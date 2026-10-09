package backend

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
)

const Version = "0.1.0"

type identity struct {
	InstanceID string
	DataEpoch  string
}

// App owns the data-directory lock and the database for the entire process.
// Lock order is Mu -> WriteMu -> FileMu; restore takes Mu exclusively.
type App struct {
	DB           *sql.DB
	DataDir      string
	Mu           sync.RWMutex
	WriteMu      sync.Mutex
	FileMu       sync.RWMutex
	maintenance  atomic.Bool
	identity     atomic.Value
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	liveJobs     sync.Map
	lock         *flock.Flock
	secret       [32]byte
	AllowedHosts map[string]bool
	// FrontendFS accepts future frontend build output, including an embed.FS.
	FrontendFS fs.FS
	closeOnce  sync.Once
	closeErr   error
	Logger     *log.Logger
	logWriter  *rotatedLog
	// Private override for deterministic persistence-failure acceptance tests.
	restoreStateWriter func(string, *restoreState) error
}

func New(dataDir string) (*App, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("数据目录不可写: %w", err)
	}
	probe, err := os.CreateTemp(dir, ".writable-")
	if err != nil {
		return nil, fmt.Errorf("数据目录不可写，请检查目录权限: %w", err)
	}
	probeName := probe.Name()
	probe.Close()
	os.Remove(probeName)
	a := &App{DataDir: dir}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.lock = flock.New(filepath.Join(dir, "runtime.lock"))
	ok, err := a.lock.TryLock()
	if err != nil || !ok {
		a.cancel()
		return nil, fmt.Errorf("数据目录已被其他实例占用或无法加锁: %v", err)
	}
	fail := func(err error) (*App, error) {
		if a.DB != nil {
			a.DB.Close()
		}
		a.cancel()
		if a.logWriter != nil {
			a.logWriter.Close()
		}
		a.lock.Unlock()
		return nil, err
	}
	for _, sub := range []string{"uploads", "tmp", "backups", "logs", "runtime"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			return fail(err)
		}
	}
	if _, err = rand.Read(a.secret[:]); err != nil {
		return fail(err)
	}
	if a.logWriter, err = openLog(dir); err != nil {
		return fail(err)
	}
	a.Logger = log.New(a.logWriter, "", log.LstdFlags|log.LUTC)
	if err = RecoverRestore(dir); err != nil {
		return fail(err)
	}
	if err = ensureDiskSpace(dir, 0); err != nil {
		return fail(err)
	}
	if a.DB, err = OpenDB(dir); err != nil {
		return fail(err)
	}
	if err = a.RefreshIdentity(); err != nil {
		return fail(err)
	}
	if err = a.RecoverFiles(); err != nil {
		return fail(err)
	}
	if err = a.RecoverTransferJobs(); err != nil {
		return fail(err)
	}
	a.StartCleanupWorker()
	return a, nil
}

func (a *App) RefreshIdentity() error {
	var state identity
	for key, target := range map[string]*string{"instance_id": &state.InstanceID, "data_epoch": &state.DataEpoch} {
		var raw string
		if err := a.DB.QueryRow("SELECT value_json FROM settings WHERE key=?", key).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), target); err != nil {
			return err
		}
	}
	a.identity.Store(state)
	return nil
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.cancel()
		a.wg.Wait()
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if a.DB != nil {
			a.closeErr = a.DB.Close()
		}
		if err := a.lock.Unlock(); a.closeErr == nil {
			a.closeErr = err
		}
		if a.logWriter != nil {
			a.logWriter.Close()
		}
	})
	return a.closeErr
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func Now() int64 { return time.Now().UTC().UnixMilli() }

func ValidID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type confirmation struct {
	Scope   string `json:"scope"`
	Hash    string `json:"hash"`
	Expires int64  `json:"expires"`
}

func (a *App) Token(scope string, payload any) string {
	b, _ := json.Marshal(payload)
	h := sha256.Sum256(b)
	body, _ := json.Marshal(confirmation{scope, hex.EncodeToString(h[:]), Now() + 10*60*1000})
	mac := hmac.New(sha256.New, a.secret[:])
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) VerifyToken(token, scope string, payload any) bool {
	var split int
	for i, c := range token {
		if c == '.' {
			split = i
			break
		}
	}
	if split == 0 {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(token[:split])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[split+1:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.secret[:])
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	var value confirmation
	if json.Unmarshal(body, &value) != nil || value.Expires < Now() || value.Scope != scope {
		return false
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	h := sha256.Sum256(b)
	return value.Hash == hex.EncodeToString(h[:])
}
