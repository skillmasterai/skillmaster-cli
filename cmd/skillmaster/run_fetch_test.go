package main

// The v78 fetch tests: what lands in the cache, what is refused before it does, and the sentences
// the two 403s produce. Split out of run_test.go when that file crossed the 1000-line guard.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
)

// **v78 A1**: the cache holds `scripts/` only. `SKILL.md` and `references/` stay on the server,
// and the hit test is the CLI's own marker, not `SKILL.md`.
func TestRunFetchesOnlyScripts(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("cd", 32)
	files := map[string]string{
		"SKILL.md":         "---\nname: hello\ndescription: d\n---\n",
		"references/a.md":  "reference",
		"scripts/hello.sh": "#!/bin/sh\necho only-scripts\n",
		"scripts/data.bin": "\x00\x01\x02",
	}
	cache := t.TempDir()
	requested := make([]string, 0, 2)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
			if index := strings.Index(r.URL.Path, "/files/"); index >= 0 {
				requested = append(requested, r.URL.Path[index+len("/files/"):])
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "sh", "hello.sh")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	if !strings.Contains(stdout, "only-scripts") {
		t.Errorf("the script did not run: %q", stdout)
	}
	for _, rel := range requested {
		if !strings.HasPrefix(rel, "scripts/") {
			t.Errorf("run fetched %q, which is outside scripts/", rel)
		}
	}
	dir := filepath.Join(cache, cacheDirName(digest))
	// **Read the marker, don't ask the code that wrote it.** `hasRunMarker` is the hit test under
	// test; using it as the assertion would let a wrong digest spelling pass on both sides.
	data, err := os.ReadFile(filepath.Join(dir, runMarker))
	if err != nil {
		t.Fatalf("the cache has no marker: %v", err)
	}
	var marker runMarkerFile
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("the marker is not JSON: %v", err)
	}
	if marker.Digest != "sha256:"+digest || marker.Rule != cacheRuleVersion {
		t.Errorf("marker = %+v, want sha256:%s rule %d", marker, digest, cacheRuleVersion)
	}
	if !isFile(filepath.Join(dir, "scripts", "hello.sh")) {
		t.Errorf("scripts/hello.sh is missing from the cache")
	}
	info, err := os.Stat(filepath.Join(dir, "scripts", "hello.sh"))
	if err != nil {
		t.Fatalf("stat scripts/hello.sh: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("scripts/hello.sh is not executable: %v", info.Mode())
	}
	if !isFile(filepath.Join(dir, "scripts", "data.bin")) {
		t.Errorf("a binary file under scripts/ did not land")
	}
	for _, gone := range []string{"SKILL.md", "references", "references/a.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s should not be in the cache (err = %v)", gone, err)
		}
	}
}

// **v78 A4**: no scripts is a refusal before anything is written.
func TestRunRefusesAVersionWithoutScripts(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("ce", 32)
	files := map[string]string{"SKILL.md": "---\nname: hello\ndescription: d\n---\n"}
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "true")
	if status == 0 {
		t.Fatalf("a version without scripts ran; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "没有可执行的脚本") {
		t.Errorf("the refusal does not name the reason: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(cache, cacheDirName(digest))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused run left a cache directory (err = %v)", err)
	}
}

// **v78 A3**: a byte that does not hash to the manifest is refused before the final directory.
func TestRunRefusesBytesThatDoNotMatchTheManifest(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("cf", 32)
	body := "#!/bin/sh\necho tampered\n"
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			_, _ = w.Write([]byte(`{"files":[{"relpath":"scripts/hello.sh","sha256":"sha256:` +
				strings.Repeat("00", 32) + `","size":` + fmt.Sprint(len(body)) +
				`,"is_binary":false}]}`))
		case strings.Contains(r.URL.Path, "/files/scripts/hello.sh"):
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "sh", "hello.sh")
	if status == 0 {
		t.Fatalf("tampered bytes ran; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "字节与清单不符") {
		t.Errorf("the refusal does not name the mismatch: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(cache, cacheDirName(digest))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a mismatched run left a cache directory (err = %v)", err)
	}
}

// **v78 A3**: the manifest must be the version this run resolved.
func TestRunRefusesAManifestForAnotherVersion(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("d0", 32)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+strings.Repeat("11", 32)+`"`)
			_, _ = w.Write([]byte(`{"files":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "true")
	if status == 0 {
		t.Fatalf("another version's manifest was accepted; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "服务端给的清单是") {
		t.Errorf("the refusal does not name the mismatch: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(cache, cacheDirName(digest))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a mismatched manifest left a cache directory (err = %v)", err)
	}
}

// **v78 A5**: the second run of the same digest asks the server for nothing.
func TestRunHitsTheCacheOnTheSecondCall(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("d1", 32)
	files := map[string]string{"scripts/hello.sh": "#!/bin/sh\necho cached\n"}
	fetches := 0
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"insufficient_scope","message":"needs skills:write"}}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
			fetches++
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	for attempt := 1; attempt <= 2; attempt++ {
		_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
			"--cache-in", cache, "--", "sh", "hello.sh")
		if status != 0 {
			t.Fatalf("run %d: status = %d, stderr = %q", attempt, status, stderr)
		}
	}
	// One manifest and one file on the first run; the second run must hit the marker.
	if fetches != 2 {
		t.Errorf("fetch requests = %d, want 2 (one fetch, then a cache hit)", fetches)
	}
}

// **The hit test is the marker's own contents**, not "the directory exists": a marker for another
// digest or another cache rule is a miss, because the rule decides what the directory holds.
func TestHasRunMarkerRequiresTheDigestAndTheRule(t *testing.T) {
	dir := t.TempDir()
	digest := strings.Repeat("d2", 32)
	if hasRunMarker(dir, digest) {
		t.Fatal("a directory with no marker was a hit")
	}
	if err := writeRunMarker(dir, digest); err != nil {
		t.Fatalf("writeRunMarker: %v", err)
	}
	if !hasRunMarker(dir, digest) {
		t.Error("a freshly written marker was not a hit")
	}
	if hasRunMarker(dir, strings.Repeat("d3", 32)) {
		t.Error("a marker for another digest was a hit")
	}
	other := []byte(`{"digest":"sha256:` + digest + `","rule":` +
		fmt.Sprint(cacheRuleVersion+1) + `}`)
	if err := os.WriteFile(filepath.Join(dir, runMarker), other, 0o644); err != nil {
		t.Fatalf("write the other marker: %v", err)
	}
	if hasRunMarker(dir, digest) {
		t.Error("a marker written under another rule was a hit")
	}
}

// **A manifest with no digest cannot be tied to the version the run resolved.** Per-file hashes
// would then bind the bytes to the listing only; the answer is to stop, as `check` already does.
func TestRunRefusesAManifestWithoutADigest(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("d4", 32)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			// No ETag: the listing is not tied to any digest.
			_, _ = w.Write([]byte(`{"files":[{"relpath":"scripts/hello.sh",` +
				`"sha256":"sha256:` + strings.Repeat("00", 32) + `","size":1,"is_binary":false}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "true")
	if status == 0 {
		t.Fatalf("a manifest with no digest was accepted; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "没有给出这一版的摘要") {
		t.Errorf("the refusal does not name the missing digest: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(cache, cacheDirName(digest))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a digest-less manifest left a cache directory (err = %v)", err)
	}
}

// **A file the manifest lists but the server will not serve stops the fetch before the rename**,
// and the error names the staging directory the half-written copy is left in (ADR 0101).
func TestRunNamesTheStagingWhenAFileCannotBeFetched(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("d5", 32)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			_, _ = w.Write([]byte(`{"files":[{"relpath":"scripts/hello.sh",` +
				`"sha256":"sha256:` + strings.Repeat("00", 32) + `","size":1,"is_binary":false}]}`))
		case strings.Contains(r.URL.Path, "/files/scripts/hello.sh"):
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "true")
	if status == 0 {
		t.Fatalf("a missing file was accepted; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "半成品留在") {
		t.Errorf("the error does not name the staging directory: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(cache, cacheDirName(digest))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed file fetch left a cache directory (err = %v)", err)
	}
}

// **The manifest is bytes from a socket.** A `..` that still starts with `scripts/` would put the
// write outside the staging directory, and `run` executes what it fetched.
func TestRunRefusesAManifestPathThatClimbsOut(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("d6", 32)
	body := "#!/bin/sh\necho escape\n"
	sum := sha256.Sum256([]byte(body))
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			_, _ = w.Write([]byte(`{"files":[{"relpath":"scripts/../../escape.sh",` +
				`"sha256":"sha256:` + hex.EncodeToString(sum[:]) + `","size":` +
				fmt.Sprint(len(body)) + `,"is_binary":false}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "true")
	if status == 0 {
		t.Fatalf("a climbing path was accepted; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "不安全") {
		t.Errorf("the refusal does not name the path: %q", stderr)
	}
}

func TestDownloadRefusalNamesTheRung(t *testing.T) {
	// **The shape the author plane actually produces**: `statusError` turns every 403 into a
	// `*api.Denial`, so a test built on `*api.ServerError` would prove nothing about the wire.
	err := downloadRefusal(&api.Denial{Code: api.Forbidden})
	if !strings.Contains(err.Error(), "写入") {
		t.Errorf("download's 403 does not name the rung: %v", err)
	}
}

func TestDownloadRefusalLeavesTheScopeAlone(t *testing.T) {
	err := downloadRefusal(&api.Denial{Code: api.InsufficientScope, Scope: "skills:write"})
	if strings.Contains(err.Error(), "整包导出需要") {
		t.Errorf("a scope failure was reported as the rung: %v", err)
	}
}

func TestCheckArchiveRefusalNamesTheRung(t *testing.T) {
	err := checkArchiveRefusal(&api.Denial{Code: api.Forbidden})
	if !strings.Contains(err.Error(), "写入") {
		t.Errorf("check's 403 does not name the rung: %v", err)
	}
}

func TestCheckListRefusalNamesTheBrowseRung(t *testing.T) {
	err := checkListRefusal(&api.Denial{Code: api.Forbidden})
	if !strings.Contains(err.Error(), "浏览") {
		t.Errorf("the manifest's 403 does not name its own rung: %v", err)
	}
	if strings.Contains(err.Error(), "写入") {
		t.Errorf("the manifest's 403 named the archive's rung: %v", err)
	}
}
