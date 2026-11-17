// `run --explain`: what this version says about running it, without running anything.
//
// **The question is the contract, not the execution.** `--explain` answers where the contract came
// from, what it delivers, and where the child would start; it materializes nothing and executes
// nothing. The closure counts come from the manifest, which is the same list the fetch filters.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/config"
)

// explainClosure is the closure's size, as `--explain` reports it.
type explainClosure struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func explainRun(ctx context.Context, address string, target runTarget, contract api.RunContract,
	source string, problems []string, command []string, opts runOptions) error {
	client, err := session(ctx)
	if err != nil {
		return err
	}
	listed, err := client.AuthorFileList(ctx, target.base, "sha256:"+target.digest)
	if err != nil {
		return versionRefusal(err)
	}
	// The same tie to the version the fetch makes: a listing that does not name this digest is not
	// this version's closure, and counting it would report a size the run would never fetch.
	served := digestPinOf(listed.ETag)
	if served == "" {
		return errors.New("服务端没有给出这一版的摘要，没法确认清单属于它。")
	}
	if strings.TrimPrefix(served, "sha256:") != target.digest {
		return archiveMismatch{fmt.Sprintf(
			"服务端给的清单是 %s，不是要的 sha256:%s。",
			served, target.digest[:min(16, len(target.digest))])}
	}
	declarations := append(append(append([]string{}, contract.Files...), contract.Path...),
		contract.Pythonpath...)
	closure := contractClosure(listed.Files, declarations)
	if len(closure) == 0 {
		// The run refuses before fetching anything, `--with` included; `--explain` must not promise
		// a closure the run will not use (v82 audit).
		return noScriptsError{name: target.base, files: declarations}
	}
	// `--with` is part of what the run would fetch, so it is part of the answer: count it, and let
	// the cwd resolution see it. A path the manifest does not hold is refused here exactly as the
	// run refuses it (v82 audit).
	for _, extra := range opts.with {
		if err := validateWithPath(extra); err != nil {
			return usagef("%v%s", err, usageAt("run"))
		}
		clean := path.Clean(extra)
		already := false
		for _, file := range closure {
			if file.Relpath == clean {
				already = true
				break
			}
		}
		if already {
			continue
		}
		var picked *api.File
		for index, file := range listed.Files {
			if file.Relpath == clean {
				picked = &listed.Files[index]
				break
			}
		}
		if picked == nil {
			return fmt.Errorf("这一版的清单里没有 %s，--with 只能补清单里的文件。", clean)
		}
		closure = append(closure, *picked)
	}
	count, bytes := closureStats(closure)
	delivered := make([]string, len(closure))
	for index, file := range closure {
		delivered[index] = file.Relpath
	}

	callerCwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("读取当前目录：%w", err)
	}
	// The command this run would execute: the caller's `--` words when given, else the contract's
	// default. `--explain -- sh tools/x.sh` must answer for that command, not for the default.
	effective := command
	fromCaller := len(command) > 0
	if !fromCaller {
		effective = contract.Default
	}
	cwd, note, uncertain, err := explainWorkingDirectory(target, contract, opts, callerCwd,
		delivered, effective, fromCaller)
	if err != nil {
		return err
	}
	shown := displayContractSource(source)

	if opts.json {
		line, err := json.Marshal(struct {
			Skill    string          `json:"skill"`
			Digest   string          `json:"digest"`
			Source   string          `json:"source"`
			Contract api.RunContract `json:"contract"`
			Closure  explainClosure  `json:"closure"`
			Cwd      string          `json:"cwd"`
			CwdMode  string          `json:"cwd_mode"`
			// True when the listing cannot settle `cwd: script`: whether a non-whitelisted token
			// can execute depends on its shebang, which is only known after fetching (v82 audit).
			CwdUncertain bool `json:"cwd_uncertain"`
			// An empty array rather than null: the field is a list, and a script iterating it should
			// not have to treat "no problems" as a different type.
			Problems []string `json:"problems"`
		}{address, "sha256:" + target.digest, shown, contract,
			explainClosure{Files: count, Bytes: bytes}, cwd, contract.Cwd, uncertain,
			append([]string{}, problems...)})
		if err != nil {
			return err
		}
		fmt.Println(string(line))
		return nil
	}

	fmt.Printf("运行契约：来源 %s\n", shown)
	for _, problem := range problems {
		fmt.Printf("  注意：%s\n", problem)
	}
	fmt.Printf("  schemaVersion: %d\n", contract.SchemaVersion)
	fmt.Printf("  files: %s\n", listOrNone(contract.Files))
	fmt.Printf("  path: %s\n", listOrNone(contract.Path))
	fmt.Printf("  pythonpath: %s\n", listOrNone(contract.Pythonpath))
	fmt.Printf("  cwd: %s\n", contract.Cwd)
	fmt.Printf("  timeoutSeconds: %d\n", contract.Limits.TimeoutSeconds)
	fmt.Printf("  default: %s\n", listOrNone(contract.Default))
	fmt.Printf("闭包：%d 个文件，%d 字节\n", count, bytes)
	if note != "" {
		fmt.Printf("工作目录：%s（%s）\n", cwd, note)
	} else {
		fmt.Printf("工作目录：%s\n", cwd)
	}
	return nil
}

// explainWorkingDirectory answers where the child would start, without materializing.
//
// **`script` is answered from the contract's own `default`.** When the version declares one, the
// command that will actually run is known here, and its script is found in the listing — the same
// relative-path/uniqueness rule the run applies — so `--explain` shows the directory a bare
// `skmr run` would use, not a fallback. With no `default` and no `--` command the directory shown
// is the version root, and the note says why. `project` and `package` are answerable exactly.
func explainWorkingDirectory(target runTarget, contract api.RunContract, opts runOptions,
	callerCwd string, files []string, command []string, fromCaller bool) (string, string, bool, error) {
	if opts.cwd != "" {
		dir, err := workingDirectory("", contract.Cwd, "", opts.cwd, callerCwd)
		return dir, "", false, err
	}
	// `project` is known without materializing anything, so `--no-cache` does not hide it.
	if contract.Cwd == "project" {
		return callerCwd, "", false, nil
	}
	if opts.noCache {
		return "", "本次用 --no-cache，运行时才知道临时目录", false, nil
	}
	// **The root the run would actually use.** A default root that fails the trust check is
	// replaced by a private temporary directory at run time, and a requested one is refused; here
	// the answer is "not knowable" rather than a path that will not be used (v82 audit).
	root := opts.cacheIn
	if root == "" {
		root = filepath.Join(os.TempDir(), config.Name, "cache")
	} else if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	if err := cacheRootTrust(root); err != nil {
		if opts.cacheIn != "" {
			return "", "缓存根 " + root + " 不可信（" + err.Error() + "）；真跑会拒绝，换个目录",
				false, nil
		}
		return "", "默认缓存根不可信，运行时会换成私有临时目录，位置运行时才知道",
			false, nil
	}
	skillDir := predictedSkillDir(opts.cacheIn, target.digest, contract)
	if contract.Cwd == "script" {
		script, ok, uncertain := defaultScript(files, command)
		if ok {
			where := "按契约的 default"
			if fromCaller {
				where = "按 -- 后面的命令"
			}
			note := where + "解析到的脚本所在目录"
			if uncertain {
				note += "；无扩展名的候选要看首行 shebang，运行时才确定"
			}
			return filepath.Dir(filepath.Join(skillDir, filepath.FromSlash(script))), note,
				uncertain, nil
		}
		return skillDir, "没有可解析的脚本（无扩展名的文件要看首行 shebang，运行时才确定），" +
			"所以是版本根；运行时若解析到脚本，会改在脚本所在目录", uncertain, nil
	}
	return skillDir, "", false, nil
}

// defaultScript finds the file a declared `default` names, as a path inside the closure.
//
// **The run's own rule, minus the filesystem.** The listing is the materialized closure's content,
// so exact relative paths and unique basenames are answerable before anything is fetched. The first
// word participates only when `PATH` cannot answer it, exactly as {@link resolveFirstWord} decides;
// later words need a script extension (a data path with a separator is the caller's own file, and
// the run leaves it alone unless it carries a shebang — which cannot be read before materializing).
func defaultScript(files []string, command []string) (string, bool, bool) {
	if len(command) == 0 {
		return "", false, false
	}
	uncertain := false
	for index, token := range command {
		if filepath.IsAbs(token) || strings.HasPrefix(token, "-") || strings.Contains(token, "://") {
			continue
		}
		scriptLike := scriptExtensions[strings.ToLower(filepath.Ext(token))]
		spelled := looksSpelled(token)
		if index == 0 {
			if !spelled {
				if _, err := exec.LookPath(token); err == nil {
					continue
				}
			}
		} else if !scriptLike {
			// The run rewrites a non-whitelisted token only when the file starts with `#!`, and
			// the listing cannot see that (v82 audit). A spelled path that exists in the closure
			// is the author naming a file, so the answer is that directory — flagged uncertain;
			// a bare word could be a data argument, so only the uncertainty is recorded.
			if script, ok := closurePath(files, token); ok {
				if spelled {
					return script, true, true
				}
				uncertain = true
			}
			continue
		}
		if script, ok := closurePath(files, token); ok {
			return script, true, uncertain || !scriptLike
		}
	}
	return "", false, uncertain
}

// closurePath resolves a token against the listing: exact relative path first, then a unique
// basename — the search order {@link lookupClosureFile} uses once the files exist.
func closurePath(files []string, token string) (string, bool) {
	cleaned := path.Clean(filepath.ToSlash(token))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	for _, file := range files {
		if file == cleaned {
			return file, true
		}
	}
	base := path.Base(cleaned)
	match, count := "", 0
	for _, file := range files {
		if path.Base(file) == base {
			match = file
			count++
		}
	}
	if count == 1 {
		return match, true
	}
	return "", false
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "（无）"
	}
	return strings.Join(values, "、")
}
