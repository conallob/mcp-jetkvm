package jetkvm

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestFirmwareContract checks the assumptions this package makes about the
// JetKVM firmware against the firmware's own source, so a protocol change
// upstream fails a test instead of silently breaking video capture or RPC on
// real devices. The fake device in fakedevice_test.go is only as accurate as
// our reading of the firmware; this test is what keeps that reading honest.
//
// It needs a checkout of https://github.com/jetkvm/kvm and is skipped without
// one:
//
//	git clone --depth 1 https://github.com/jetkvm/kvm /tmp/jetkvm-kvm
//	JETKVM_FIRMWARE_DIR=/tmp/jetkvm-kvm go test -run TestFirmwareContract ./internal/jetkvm
//
// The firmware-contract workflow runs it weekly against main and the latest
// release.
func TestFirmwareContract(t *testing.T) {
	dir := os.Getenv("JETKVM_FIRMWARE_DIR")
	if dir == "" {
		t.Skip("JETKVM_FIRMWARE_DIR not set; see the test's doc comment")
	}
	fw := readFirmwareSources(t, dir)

	t.Run("login endpoint and cookie", func(t *testing.T) {
		fw.mustContain(t, `"/auth/login-local"`)
		// The signaling socket checks this cookie, which the login sets.
		fw.mustContain(t, `Cookie("authToken")`)
	})

	t.Run("signaling WebSocket path", func(t *testing.T) {
		// The path is a const (localSignalingPath) on main and a literal in
		// the route on stable releases, so accept either spelling.
		fw.mustContain(t, `"`+signalingPath+`"`)
		fw.mustMatch(t, `protected\.GET\((localSignalingPath|"`+regexp.QuoteMeta(signalingPath)+`"),`)
	})

	t.Run("signaling messages", func(t *testing.T) {
		// Device -> client greeting, answer, and trickled candidates.
		fw.mustMatch(t, `"type":\s*"device-metadata"`)
		fw.mustMatch(t, `"type":\s*"answer",\s*"data":`)
		fw.mustMatch(t, `"type":\s*"new-ice-candidate"`)
		// Client -> device offer, carrying {"sd": base64}.
		fw.mustMatch(t, `message\.Type == "offer"`)
		fw.mustMatch(t, "Sd\\s+string\\s+`json:\"sd\"`")
	})

	t.Run("RPC data channel label", func(t *testing.T) {
		fw.mustMatch(t, `case "`+rpcChannelLabel+`":`)
	})

	t.Run("RPC methods used by this package exist", func(t *testing.T) {
		used := rpcMethodsUsed(t)
		if len(used) == 0 {
			t.Fatal("found no RPC methods in this package; the extraction regexp is stale")
		}
		var missing []string
		for _, m := range used {
			if !regexp.MustCompile(`(?m)^\s*"` + regexp.QuoteMeta(m) + `":\s*\{`).MatchString(fw.rpcHandlers) {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			t.Errorf("RPC methods missing from the firmware's rpcHandlers table: %s", strings.Join(missing, ", "))
		}
	})

	t.Run("legacy HTTP session endpoint", func(t *testing.T) {
		// Only a fallback for older firmware, so informational: its absence
		// means the fallback in ExchangeSDP no longer serves any device that
		// this firmware line supports.
		if !strings.Contains(fw.all, `"`+legacySessionPath+`"`) {
			t.Logf("firmware no longer serves %s; the legacy fallback is dead code for this release", legacySessionPath)
		}
	})
}

// firmwareSources is the firmware's root-package Go source.
type firmwareSources struct {
	all         string // every non-test root .go file, concatenated
	rpcHandlers string // the body of the rpcHandlers table in jsonrpc.go
}

func readFirmwareSources(t *testing.T, dir string) firmwareSources {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in JETKVM_FIRMWARE_DIR=%q (is it a jetkvm/kvm checkout?)", dir)
	}
	var b strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	all := b.String()

	_, after, ok := strings.Cut(all, "var rpcHandlers = map[string]RPCHandler{")
	if !ok {
		t.Fatal("rpcHandlers table not found in the firmware source; the RPC dispatch has been restructured")
	}
	return firmwareSources{all: all, rpcHandlers: after}
}

func (f firmwareSources) mustContain(t *testing.T, s string) {
	t.Helper()
	if !strings.Contains(f.all, s) {
		t.Errorf("firmware source no longer contains %s", s)
	}
}

func (f firmwareSources) mustMatch(t *testing.T, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(f.all) {
		t.Errorf("firmware source no longer matches /%s/", pattern)
	}
}

// rpcMethodsUsed returns the RPC method names this package's non-test code
// calls, so the list cannot drift from the code.
func rpcMethodsUsed(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	re := regexp.MustCompile(`callRPC\([^,]+,\s*[^,]+,\s*"([A-Za-z]+)"`)
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			seen[m[1]] = true
		}
	}
	methods := make([]string, 0, len(seen))
	for m := range seen {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	return methods
}
