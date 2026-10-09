package config

import (
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

type Config struct {
	Host                          string
	Port                          int
	DataDir, WebDir               string
	Portable, NoOpen, ShowVersion bool
}

func Parse(args []string) (Config, error) {
	var c Config
	host := os.Getenv("LJMDB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("LJMDB_PORT")
	if port == "" {
		port = "8080"
	}
	dir := os.Getenv("LJMDB_DATA_DIR")
	fs := flag.NewFlagSet("ljmdb", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.Host, "host", host, "只允许回环地址")
	fs.StringVar(&port, "port", port, "HTTP 端口")
	fs.StringVar(&c.DataDir, "data-dir", dir, "数据目录")
	fs.StringVar(&c.WebDir, "web-dir", "", "可选的前端构建产物目录")
	fs.BoolVar(&c.Portable, "portable", false, "可执行文件同级 data 目录")
	fs.BoolVar(&c.NoOpen, "no-open", false, "不打开浏览器")
	fs.BoolVar(&c.ShowVersion, "version", false, "显示版本")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, errors.New("不支持位置参数")
	}
	if c.ShowVersion {
		return c, nil
	}
	var err error
	c.Port, err = strconv.Atoi(port)
	if err != nil {
		return c, errors.New("端口必须为有效整数")
	}
	ip := net.ParseIP(c.Host)
	if ip == nil || !ip.IsLoopback() {
		return c, errors.New("首版仅支持回环 IP（127.0.0.1 或 ::1），局域网模式尚未启用")
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, errors.New("端口必须为 1 到 65535")
	}
	if c.Portable && c.DataDir != "" {
		return c, errors.New("--portable 与 --data-dir / LJMDB_DATA_DIR 不能同时使用")
	}
	if c.Portable {
		exe, err := os.Executable()
		if err != nil {
			return c, err
		}
		c.DataDir = filepath.Join(filepath.Dir(exe), "data")
	}
	if c.DataDir == "" {
		var err error
		c.DataDir, err = DefaultDataDir()
		if err != nil {
			return c, err
		}
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return c, err
	}
	c.DataDir = abs
	return c, nil
}

func DefaultDataDir() (string, error) {
	if runtime.GOOS == "linux" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "share")
		}
		if !filepath.IsAbs(base) {
			return "", errors.New("XDG_DATA_HOME 必须为绝对路径")
		}
		return filepath.Join(base, "LJMDB"), nil
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "LJMDB"), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		base = local
	}
	return filepath.Join(base, "LJMDB"), nil
}
