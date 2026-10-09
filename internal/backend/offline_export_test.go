package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Optional real-browser acceptance for the backend's portable HTML export.
// LJMDB_PLAYWRIGHT_MODULE can point to any existing Playwright installation;
// this test neither installs dependencies nor modifies application frontend files.
func TestOfflineHTMLExportInBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js unavailable for optional browser acceptance")
	}
	module := os.Getenv("LJMDB_PLAYWRIGHT_MODULE")
	if module == "" {
		module = "playwright"
	}
	probe := exec.Command(node, "-e", "require(process.argv[1])", module)
	if out, err := probe.CombinedOutput(); err != nil {
		if os.Getenv("LJMDB_BROWSER_TESTS") == "1" {
			t.Fatalf("Playwright unavailable: %v %s", err, out)
		}
		t.Skip("Playwright unavailable for optional browser acceptance")
	}
	browser := ""
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if binary, err := exec.LookPath(name); err == nil {
			browser = binary
			break
		}
	}
	a := coreApp(t)
	kb := coreCreateKB(t, a)
	picture := image.NewRGBA(image.Rect(0, 0, 3, 2))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, picture); err != nil {
		t.Fatal(err)
	}
	attachment, err := a.PrepareAttachment(bytes.NewReader(pngBytes.Bytes()), "本地图片.png", MaxAttachmentBytes)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = InsertAttachment(context.Background(), tx, attachment); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	markdown := "# 重复标题\n\n# 重复标题\n\n公式 $a^2+b^2=c^2$\n\n$$\n\\int_0^1 x^2 dx = \\frac{1}{3}\n$$\n\n```mermaid\ngraph TD\nA[开始] --> B[结束]\n```\n\n```mermaid\nsequenceDiagram\nAlice->>Bob: hello\nBob-->>Alice: response\n```\n\n```go\nfmt.Println(\"<unsafe>\")\n```\n\n![本地图片](/api/v1/attachments/" + attachment.ID + "/content)\n"
	d := coreCreateDoc(t, a, kb, nil, "离线导出验证", markdown)
	relative, _, err := a.buildExport(context.Background(), NewID(), exportRequest{Scope: "document", ID: d.ID, Format: "html_zip"})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if _, err = extractArchive(filepath.Join(a.DataDir, filepath.FromSlash(relative)), dest, archiveLimits{Bytes: 128 << 20, Files: 1000, Depth: 12}); err != nil {
		t.Fatal(err)
	}
	var manifest ContentManifest
	if err = readJSONFile(filepath.Join(dest, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Documents) != 1 {
		t.Fatal(manifest)
	}
	entry := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(dest, filepath.FromSlash(manifest.Documents[0].Path)))}
	const script = `
const {chromium} = require(process.argv[1]);
(async () => {
 const args = {headless:true,args:['--no-sandbox','--disable-background-networking']};
 if (process.argv[3]) args.executablePath=process.argv[3];
 const browser=await chromium.launch(args);
 try {
  const context=await browser.newContext({offline:true});
  const network=[],errors=[];
  const page=await context.newPage();
  page.on('request',request=>{if(/^https?:/.test(request.url()))network.push(request.url());});
  page.on('pageerror',error=>errors.push(error.message));
  await context.route('**/*',route=>/^https?:/.test(route.request().url())?route.abort():route.continue());
  await page.goto(process.argv[2],{waitUntil:'load'});
  await page.waitForFunction(()=>document.querySelectorAll('.katex').length>=2 && document.querySelectorAll('.language-mermaid svg').length>=2,{timeout:20000});
  const result=await page.evaluate(async()=>{
   await document.fonts.ready;
   const img=document.querySelector('main img');
   const headings=[...document.querySelectorAll('main h1')].map(node=>node.id);
   return {formulas:document.querySelectorAll('.katex').length,diagrams:document.querySelectorAll('.language-mermaid svg').length,imageLoaded:!!img&&img.complete&&img.naturalWidth===3,highlighted:document.querySelectorAll('pre code span').length>0,headingsDistinct:headings.length===2&&headings[0]!==headings[1],fontLoaded:document.fonts.check('16px KaTeX_Main'),sourceErrors:document.querySelectorAll('[data-render-error]').length};
  });
  result.network=network;result.errors=errors;
  console.log(JSON.stringify(result));
 } finally {await browser.close();}
})().catch(error=>{console.error(error.stack);process.exit(1);});`
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", script, module, entry.String(), browser)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("offline file:// export failed: %v\n%s", err, out)
	}
	var result struct {
		Formulas, Diagrams, SourceErrors                       int
		ImageLoaded, Highlighted, HeadingsDistinct, FontLoaded bool
		Network, Errors                                        []string
	}
	if err = json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		t.Fatalf("browser result malformed: %s", out)
	}
	if result.Formulas < 2 || result.Diagrams < 2 || !result.ImageLoaded || !result.Highlighted || !result.HeadingsDistinct || !result.FontLoaded || result.SourceErrors != 0 || len(result.Network) != 0 || len(result.Errors) != 0 {
		t.Fatalf("offline export acceptance failed: %s", out)
	}
	t.Logf("offline file:// browser acceptance: %s", out)
}
