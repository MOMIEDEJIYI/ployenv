package runtime

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// The two indexes below are trimmed copies of the real shapes: the Go one is
// what go.dev/dl answers with mode=json, the Node one is nodejs.org/dist/index.json.
const goIndexJSON = `[
  {"version":"go1.27.2","stable":true,"files":[
    {"filename":"go1.27.2.windows-amd64.zip","os":"windows","arch":"amd64","kind":"archive","sha256":"11f27"},
    {"filename":"go1.27.2.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"11727"},
    {"filename":"go1.27.2.darwin-arm64.pkg","os":"darwin","arch":"arm64","kind":"installer","sha256":"dd127"}
  ]},
  {"version":"go1.26.4","stable":true,"files":[
    {"filename":"go1.26.4.windows-amd64.zip","os":"windows","arch":"amd64","kind":"archive","sha256":"11626"},
    {"filename":"go1.26.4.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"11626l"}
  ]},
  {"version":"go1.28rc1","stable":false,"files":[
    {"filename":"go1.28rc1.windows-amd64.zip","os":"windows","arch":"amd64","kind":"installer","sha256":"1128"}
  ]}
]`

const nodeIndexJSON = `[
  {"version":"v22.11.0","lts":"Jod"},
  {"version":"v23.1.0","lts":false},
  {"version":"v20.18.0","lts":"Iron"}
]`

func goIndexServer(t *testing.T) string {
	t.Helper()
	srv := staticServer(t, map[string]string{"/": goIndexJSON})
	writeConfig(t, map[string]string{"go": srv.URL})
	return srv.URL
}

func nodeIndexServer(t *testing.T) string {
	t.Helper()
	srv := staticServer(t, map[string]string{"/index.json": nodeIndexJSON})
	writeConfig(t, map[string]string{"node": srv.URL})
	return srv.URL
}

func TestGoListFiltersAndMarksPreviews(t *testing.T) {
	goIndexServer(t)

	all, err := goList("")
	if err != nil {
		t.Fatalf("goList(\"\"): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("goList(\"\") returned %d releases, want 3", len(all))
	}
	if all[0].Version != "1.27.2" || all[0].Note != "" {
		t.Fatalf("stable release = %+v, want version 1.27.2 with no note", all[0])
	}
	if all[2].Note != "preview" {
		t.Fatalf("unstable release note = %q, want preview", all[2].Note)
	}

	stable, err := goList("stable")
	if err != nil {
		t.Fatalf("goList(stable): %v", err)
	}
	if len(stable) != 2 {
		t.Fatalf("goList(stable) returned %d releases, want 2", len(stable))
	}

	// A channel keyword must behave like "everything", not like a version.
	for _, channel := range []string{"all", "latest"} {
		got, err := goList(channel)
		if err != nil {
			t.Fatalf("goList(%s): %v", channel, err)
		}
		if len(got) != 3 {
			t.Fatalf("goList(%s) returned %d releases, want 3", channel, len(got))
		}
	}

	one, err := goList("1.27")
	if err != nil {
		t.Fatalf("goList(1.27): %v", err)
	}
	if len(one) != 1 || one[0].Version != "1.27.2" {
		t.Fatalf("goList(1.27) = %+v, want just 1.27.2", one)
	}

	none, err := goList("9.9.9")
	if err != nil {
		t.Fatalf("goList(9.9.9): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("goList(9.9.9) = %+v, want nothing", none)
	}
}

func TestGoPlanPicksTheArchiveForThisPlatform(t *testing.T) {
	base := goIndexServer(t)

	plan, err := goPlan("go1.27.2")
	if err != nil {
		t.Fatalf("goPlan: %v", err)
	}
	if plan.Exact != "1.27.2" {
		t.Fatalf("Exact = %q, want the version without its go prefix", plan.Exact)
	}
	if plan.Layout != LayoutFlatten {
		t.Fatalf("Layout = %v, want LayoutFlatten", plan.Layout)
	}
	if got := plan.URLFor(base); got != base+"/"+plan.File {
		t.Fatalf("URLFor = %q, want %q", got, base+"/"+plan.File)
	}
	if len(plan.Bases) == 0 || plan.Bases[0] != base {
		t.Fatalf("Bases = %v, want the configured mirror first", plan.Bases)
	}
	if plan.SHA256 == "" {
		t.Fatal("the index publishes a sha256, so the plan must carry it")
	}
	switch platform.OS() + "/" + platform.Arch() {
	case "windows/amd64":
		if plan.File != "go1.27.2.windows-amd64.zip" {
			t.Fatalf("File = %q", plan.File)
		}
	case "linux/amd64":
		if plan.File != "go1.27.2.linux-amd64.tar.gz" {
			t.Fatalf("File = %q", plan.File)
		}
	}

	// The version exists but only as an installer: no archive, so no plan.
	if _, err := goPlan("1.28rc1"); err == nil {
		t.Fatal("goPlan must fail when the release has no archive for this platform")
	}
	// Unknown version.
	if _, err := goPlan("9.9.9"); err == nil {
		t.Fatal("goPlan must fail for a version the index does not list")
	}
}

func TestNodeListFiltersLTSAndPlansArchives(t *testing.T) {
	base := nodeIndexServer(t)

	all, err := nodeList("")
	if err != nil {
		t.Fatalf("nodeList(\"\"): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("nodeList(\"\") returned %d releases, want 3", len(all))
	}
	if all[0].Version != "22.11.0" || all[0].LTS != "Jod" || all[0].Note != "LTS Jod" {
		t.Fatalf("first release = %+v", all[0])
	}
	if all[1].LTS != "" || all[1].Note != "" {
		t.Fatalf("a release with lts=false must have no LTS label: %+v", all[1])
	}

	lts, err := nodeList("lts")
	if err != nil {
		t.Fatalf("nodeList(lts): %v", err)
	}
	if len(lts) != 2 {
		t.Fatalf("nodeList(lts) returned %d releases, want 2", len(lts))
	}

	for _, filter := range []string{"20", "v22", "22.11.0"} {
		got, err := nodeList(filter)
		if err != nil {
			t.Fatalf("nodeList(%s): %v", filter, err)
		}
		if len(got) != 1 {
			t.Fatalf("nodeList(%s) returned %d releases, want 1", filter, len(got))
		}
	}
	if got, err := nodeList("lts-x"); err != nil || len(got) != 0 {
		t.Fatalf("nodeList(lts-x) = %v, %v; want nothing", got, err)
	}

	plan, err := nodePlan("v22.11.0")
	if err != nil {
		t.Fatalf("nodePlan: %v", err)
	}
	if plan.Exact != "22.11.0" {
		t.Fatalf("Exact = %q", plan.Exact)
	}
	if plan.Layout != LayoutFlatten {
		t.Fatalf("Layout = %v", plan.Layout)
	}
	ext := "tar.gz"
	osName := map[string]string{"windows": "win", "darwin": "darwin", "linux": "linux"}[platform.OS()]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[platform.Arch()]
	if platform.IsWindows() {
		ext = "zip"
	}
	wantFile := fmt.Sprintf("node-v22.11.0-%s-%s.%s", osName, arch, ext)
	if plan.File != wantFile {
		t.Fatalf("File = %q, want %q", plan.File, wantFile)
	}
	if got := plan.URLFor(base); got != base+"/v22.11.0/"+wantFile {
		t.Fatalf("URLFor = %q", got)
	}
	if plan.Exact == "v22.11.0" {
		t.Fatal("the recorded version must not keep the v prefix")
	}

	// The v prefix is optional on input as well.
	if _, err := nodePlan("22.11.0"); err != nil {
		t.Fatalf("nodePlan without the v prefix: %v", err)
	}
}

// A configured mirror is only useful if the index is actually requested from it
// (FR-6), so make the assertion explicit by recording what the server saw.
func TestIndexRequestsGoToTheConfiguredMirror(t *testing.T) {
	var seen []string
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/":
			w.Write([]byte(goIndexJSON))
		case "/index.json":
			w.Write([]byte(nodeIndexJSON))
		default:
			http.NotFound(w, r)
		}
	})
	writeConfig(t, map[string]string{"go": srv.URL, "node": srv.URL})

	if _, err := goList("stable"); err != nil {
		t.Fatalf("goList: %v", err)
	}
	if _, err := nodeList("lts"); err != nil {
		t.Fatalf("nodeList: %v", err)
	}

	var goReq, nodeReq bool
	for _, s := range seen {
		if strings.HasPrefix(s, "/?mode=json&include=all") {
			goReq = true
		}
		if s == "/index.json?" {
			nodeReq = true
		}
	}
	if !goReq {
		t.Fatalf("the Go index was not requested from the mirror: %v", seen)
	}
	if !nodeReq {
		t.Fatalf("the Node index was not requested from the mirror: %v", seen)
	}
}
