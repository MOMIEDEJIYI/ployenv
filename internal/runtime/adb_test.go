package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adbRepositoryXML is trimmed from the real repository2-3.xml: the sdk
// namespace on the root, one platform-tools package with the three per-OS
// archives, and the SHA-1-only checksum shape the repository publishes.
const adbRepositoryXML = `<?xml version="1.0" encoding="utf-8"?>
<sdk:sdk-repository xmlns:sdk="http://schemas.android.com/sdk/android/repo/repository2/03">
  <remotePackage path="platform-tools">
    <type-details xsi:type="generic:genericDetailsType"/>
    <revision>
      <major>37</major>
      <minor>0</minor>
      <micro>1</micro>
    </revision>
    <display-name>Android SDK Platform-Tools</display-name>
    <channelRef ref="channel-0"/>
    <archives>
      <archive>
        <complete>
          <size>9054187</size>
          <checksum type="sha1">477254aa5f903c15cf51001717bdf347fb6b53e0</checksum>
          <url>platform-tools_r37.0.1-linux.zip</url>
        </complete>
        <host-os>linux</host-os>
      </archive>
      <archive>
        <complete>
          <size>16110554</size>
          <checksum type="sha1">6ae73f4de6452dc57e62ec02b68eed92a4c21661</checksum>
          <url>platform-tools_r37.0.1-darwin.zip</url>
        </complete>
        <host-os>macosx</host-os>
      </archive>
      <archive>
        <complete>
          <size>8044989</size>
          <checksum type="sha1">e03e78b1d80b396f1c3358e31251cb31740e1110</checksum>
          <url>platform-tools_r37.0.1-win.zip</url>
        </complete>
        <host-os>windows</host-os>
      </archive>
    </archives>
  </remotePackage>
  <remotePackage path="emulators;16606167">
    <revision>
      <major>36</major>
      <minor>1</minor>
      <micro>3</micro>
    </revision>
  </remotePackage>
</sdk:sdk-repository>`

// adbIndexServer points every Android endpoint at a local server, so no test
// ever reaches the network.
func adbIndexServer(t *testing.T) string {
	t.Helper()
	srv := staticServer(t, map[string]string{"/repository2-3.xml": adbRepositoryXML})
	override(t, &androidRepository, srv.URL)
	old := androidMirrors
	androidMirrors = nil
	t.Cleanup(func() { androidMirrors = old })
	return srv.URL
}

func TestADBListsTheIndexedPlatformToolsOnly(t *testing.T) {
	adbIndexServer(t)

	for _, filter := range []string{"", "latest", "stable", "release", "all", "37", "37.0"} {
		rels, err := adbList(filter)
		if err != nil {
			t.Fatalf("adbList(%q): %v", filter, err)
		}
		if len(rels) != 1 || rels[0].Version != "37.0.1" {
			t.Errorf("adbList(%q) = %v, want the single indexed 37.0.1", filter, rels)
		}
	}
	for _, filter := range []string{"36", "37.0.0", "34.0.5"} {
		rels, err := adbList(filter)
		if err != nil {
			t.Fatalf("adbList(%q): %v", filter, err)
		}
		if len(rels) != 0 {
			t.Errorf("adbList(%q) = %v, want nothing: the repository carries one revision", filter, rels)
		}
	}
	if _, err := adbList("lts"); err == nil || !strings.Contains(err.Error(), "no LTS channel") {
		t.Errorf("adbList(lts) error = %v, want the no-LTS message", err)
	}
}

// The repository's two platform names must stay in step: <host-os> in the index
// and the archive suffix in the file name.
func TestADBArchiveNamePerOS(t *testing.T) {
	cases := map[string]struct{ name, hostOS string }{
		"windows": {"platform-tools_r37.0.1-win.zip", "windows"},
		"darwin":  {"platform-tools_r37.0.1-darwin.zip", "macosx"},
		"linux":   {"platform-tools_r37.0.1-linux.zip", "linux"},
	}
	for osName, want := range cases {
		if got := adbArchiveName(osName, "37.0.1"); got != want.name {
			t.Errorf("adbArchiveName(%s) = %q, want %q", osName, got, want.name)
		}
		if got := adbHostOS(osName); got != want.hostOS {
			t.Errorf("adbHostOS(%s) = %q, want %q", osName, got, want.hostOS)
		}
	}
}

func TestADBPlanCarriesTheIndexedSHA1(t *testing.T) {
	adbIndexServer(t)
	wantSHA1 := map[string]string{
		"windows": "e03e78b1d80b396f1c3358e31251cb31740e1110",
		"darwin":  "6ae73f4de6452dc57e62ec02b68eed92a4c21661",
		"linux":   "477254aa5f903c15cf51001717bdf347fb6b53e0",
	}
	for osName, sum := range wantSHA1 {
		withOS(t, osName)
		plan, err := adbSpec.Plan("37.0.1")
		if err != nil {
			t.Fatalf("Plan(%s): %v", osName, err)
		}
		if plan.Exact != "37.0.1" {
			t.Errorf("%s: Exact = %q, want 37.0.1", osName, plan.Exact)
		}
		if plan.File != adbArchiveName(osName, "37.0.1") {
			t.Errorf("%s: File = %q, want %q", osName, plan.File, adbArchiveName(osName, "37.0.1"))
		}
		if plan.SHA1 != sum {
			t.Errorf("%s: SHA1 = %q, want %q", osName, plan.SHA1, sum)
		}
		if plan.Layout != LayoutFlatten {
			t.Errorf("%s: Layout = %v, want LayoutFlatten (single platform-tools/ folder)", osName, plan.Layout)
		}
		if plan.SHA256 != "" || plan.SHA512 != "" {
			t.Errorf("%s: the repository publishes SHA-1 only", osName)
		}
		if got := plan.URLFor("https://example.test/repo/"); got != "https://example.test/repo/"+plan.File {
			t.Errorf("%s: URLFor = %q", osName, got)
		}
	}
}

// A version the index has moved past still installs from the URL convention -
// unverified, and said out loud rather than failed.
func TestADBPlanWithoutAnIndexedChecksum(t *testing.T) {
	adbIndexServer(t)
	withOS(t, "windows")

	plan, err := adbSpec.Plan("36.0.0")
	if err != nil {
		t.Fatalf("Plan(36.0.0): %v", err)
	}
	if plan.SHA1 != "" {
		t.Errorf("SHA1 = %q, want empty for a version the index does not carry", plan.SHA1)
	}
	if plan.File != "platform-tools_r36.0.0-win.zip" {
		t.Errorf("File = %q, want the conventional name", plan.File)
	}
}

func TestADBVerifyReadsTheVersionStamp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "37.0.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stamp := filepath.Join(dir, "source.properties")
	if err := os.WriteFile(stamp, []byte("Pkg.UserSrc=false\nPkg.Revision=37.0.1\n"), 0o644); err != nil {
		t.Fatalf("writing source.properties: %v", err)
	}

	got, err := adbSpec.Verify(dir)
	if err != nil || got != "37.0.1" {
		t.Fatalf("Verify = (%q, %v), want (37.0.1, nil)", got, err)
	}

	// A stamp that disagrees with the version directory is how a mislabelled
	// archive shows up; the install must say so.
	if err := os.WriteFile(stamp, []byte("Pkg.Revision=36.0.0\n"), 0o644); err != nil {
		t.Fatalf("rewriting source.properties: %v", err)
	}
	if _, err := adbSpec.Verify(dir); err == nil || !strings.Contains(err.Error(), "36.0.0") {
		t.Errorf("Verify with a mismatched stamp = %v, want an error naming it", err)
	}

	if err := os.Remove(stamp); err != nil {
		t.Fatalf("removing source.properties: %v", err)
	}
	if _, err := adbSpec.Verify(dir); err == nil {
		t.Error("Verify without source.properties must fail")
	}
}

func TestADBToolPathsFollowTheFlatLayout(t *testing.T) {
	for _, tool := range []string{"adb", "fastboot"} {
		if got := adbToolPathFor("windows", tool); got != tool+".exe" {
			t.Errorf("adbToolPathFor(windows, %s) = %q", tool, got)
		}
		if got := adbToolPathFor("linux", tool); got != tool {
			t.Errorf("adbToolPathFor(linux, %s) = %q", tool, got)
		}
		if got := adbToolPathFor("darwin", tool); got != tool {
			t.Errorf("adbToolPathFor(darwin, %s) = %q", tool, got)
		}
	}
	if got := adbToolPathFor("windows", "hprof-conv"); got != "" {
		t.Errorf("an unexposed tool must have no path, got %q", got)
	}
}

// platform-tools is a component, not an SDK root: activating a version must
// never invent ANDROID_HOME or ANDROID_SDK_ROOT.
func TestADBInjectsNoAndroidHome(t *testing.T) {
	entries := adbSpec.EnvEntries(filepath.Join("C:", "data", "versions", "adb", "37.0.1"))
	if len(entries) != 0 {
		t.Errorf("EnvEntries = %v, want nothing (PATH only)", entries)
	}
	dirs := adbSpec.PathEntries(filepath.Join("C:", "data", "versions", "adb", "37.0.1"))
	want := filepath.Join("C:", "data", "versions", "adb", "37.0.1")
	if len(dirs) != 1 || dirs[0] != want {
		t.Errorf("PathEntries = %v, want [%q]", dirs, want)
	}
}
