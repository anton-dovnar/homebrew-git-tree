package structs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
)

// ResolveGitDir attempts to resolve the repository's git directory.
//
// It supports:
// - standard repos where ".git" is a directory
// - worktrees/submodules where ".git" is a file containing "gitdir: <path>"
// - being called from any subdirectory of the repo (walks parents)
func ResolveGitDir(startPath string) (string, error) {
	if startPath == "" {
		return "", errors.New("empty path")
	}

	p := filepath.Clean(startPath)
	for {
		dotgit := filepath.Join(p, ".git")
		fi, err := os.Stat(dotgit)
		if err == nil {
			if fi.IsDir() {
				return dotgit, nil
			}
			// .git is a file: read "gitdir: <path>"
			b, rerr := os.ReadFile(dotgit)
			if rerr != nil {
				return "", fmt.Errorf("read %s: %w", dotgit, rerr)
			}
			s := strings.TrimSpace(string(b))
			if strings.HasPrefix(s, "gitdir:") {
				gd := strings.TrimSpace(strings.TrimPrefix(s, "gitdir:"))
				if gd == "" {
					return "", fmt.Errorf("invalid gitdir in %s", dotgit)
				}
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(p, gd)
				}
				return filepath.Clean(gd), nil
			}
			return "", fmt.Errorf("unrecognized .git file format: %s", dotgit)
		}

		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}

	return "", fmt.Errorf("could not find .git starting at %s", startPath)
}

// ReadReflogNewHashes reads reflog entries for a ref and returns the new hashes
// in file order. Missing reflog files return (nil, nil).
//
// refName is expected to be a full ref name like "refs/heads/main" or
// "refs/remotes/origin/feature".
func ReadReflogNewHashes(gitDir, refName string) ([]plumbing.Hash, error) {
	if gitDir == "" || refName == "" {
		return nil, errors.New("empty gitDir or refName")
	}
	path := filepath.Join(gitDir, "logs", filepath.FromSlash(refName))
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open reflog %s: %w", path, err)
	}
	defer f.Close()

	var out []plumbing.Hash
	seen := make(map[plumbing.Hash]struct{})
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Format: <old> <new> <author> <timestamp> <tz>\t<message>
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		newHex := fields[1]
		if len(newHex) != 40 {
			continue
		}
		h := plumbing.NewHash(newHex)
		if h.IsZero() {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan reflog %s: %w", path, err)
	}
	return out, nil
}

// TrackedRemoteRefs returns the set of remote refs tracked by local branches,
// e.g. "refs/remotes/origin/main".
//
// This excluding tracked remote refs from the
// "extra remote reflog labelling" when --all is enabled.
func TrackedRemoteRefs(gitDir string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if gitDir == "" {
		return out, errors.New("empty gitDir")
	}

	cfgPath := filepath.Join(gitDir, "config")
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read git config %s: %w", cfgPath, err)
	}

	type branchCfg struct {
		remote string
		merge  string
	}

	branches := make(map[string]*branchCfg)
	var curBranch string

	lines := strings.Split(string(b), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			curBranch = ""
			sec := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			// Example: branch "main"
			if strings.HasPrefix(sec, "branch ") {
				rest := strings.TrimSpace(strings.TrimPrefix(sec, "branch "))
				rest = strings.Trim(rest, `"`)
				if rest != "" {
					curBranch = rest
					if branches[curBranch] == nil {
						branches[curBranch] = &branchCfg{}
					}
				}
			}
			continue
		}

		if curBranch == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		bc := branches[curBranch]
		switch key {
		case "remote":
			bc.remote = val
		case "merge":
			bc.merge = val
		}
	}

	for _, bc := range branches {
		if bc == nil || bc.remote == "" || bc.merge == "" {
			continue
		}
		merge := bc.merge
		merge = strings.TrimPrefix(merge, "refs/heads/")
		if merge == "" {
			continue
		}
		out[fmt.Sprintf("refs/remotes/%s/%s", bc.remote, merge)] = struct{}{}
	}

	return out, nil
}
