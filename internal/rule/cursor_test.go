package rule

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// **Cursor's rule files are machine-local `.mdc` files.** Detection has to look at every one in
// `~/.cursor/rules`, because the person may name theirs anything (measured 2026-11-26).
func TestDefaultPresentFindsCursorsRuleFile(t *testing.T) {
	home := t.TempDir()
	rules := filepath.Join(home, ".cursor", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "other.mdc"), []byte("---\nalwaysApply: true\n---\n别的规则\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, known := DefaultPresent(home); present || !known {
		t.Fatalf("a rule file without the block: present=%v known=%v; want false, true", present, known)
	}
	if err := os.WriteFile(filepath.Join(rules, "skillmaster.mdc"), append([]byte("---\nalwaysApply: true\n---\n\n"), Block()...), 0o644); err != nil {
		t.Fatal(err)
	}
	if present, _ := DefaultPresent(home); !present {
		t.Fatal("the rule inside ~/.cursor/rules/*.mdc was not detected")
	}
}

// **A `.mdc` without frontmatter is ignored by Cursor**, so the install says so instead of reporting
// a clean success that changes nothing the agent sees.
func TestInstallWarnsAboutAMDCWithoutFrontmatter(t *testing.T) {
	dir := tempDir(t)
	plain := filepath.Join(dir, "skillmaster.mdc")
	if err := os.WriteFile(plain, []byte("原来的内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(dir, plain, "cursor", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Notice == "" {
		t.Error("a .mdc without frontmatter was written without a warning")
	}

	withFrontmatter := filepath.Join(dir, "with-frontmatter.mdc")
	header := "---\ndescription: 测试\nalwaysApply: true\n---\n\n"
	if err := os.WriteFile(withFrontmatter, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Install(dir, withFrontmatter, "cursor", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Notice != "" {
		t.Errorf("a .mdc with frontmatter carried a warning: %q", res.Notice)
	}
	if blob := readFile(t, withFrontmatter); !bytes.HasPrefix(blob, []byte(header)) {
		t.Errorf("the frontmatter did not survive the write:\n%s", blob)
	}
}

// **The bytes are there and Cursor reads none of them.** `install --check` saying 「已安装」 for a
// frontmatter-less `.mdc` is the silent failure v94 exists to end, and the report must not count it
// as coverage either.
func TestAFrontmatterlessCursorRuleIsCalledIgnored(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "skillmaster.mdc")
	if err := os.WriteFile(path, []byte("原来的内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, path, "cursor", false); err != nil {
		t.Fatal(err)
	}
	if FileHasRule(path) {
		t.Error("a frontmatter-less .mdc was counted as coverage")
	}
	statuses, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "ignored" {
		t.Fatalf("statuses = %+v; want one ignored", statuses)
	}

	// Adding the frontmatter by hand is the documented remedy: the same file then counts.
	blob := readFile(t, path)
	fixed := append([]byte("---\ndescription: 测试\nalwaysApply: true\n---\n\n"), blob...)
	if err := os.WriteFile(path, fixed, 0o644); err != nil {
		t.Fatal(err)
	}
	if !FileHasRule(path) {
		t.Error("a .mdc with frontmatter was still not counted as coverage")
	}
	statuses, err = Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != "present" {
		t.Fatalf("statuses = %+v; want one present", statuses)
	}
}
