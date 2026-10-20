package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type manifest struct {
	Version   int             `json:"version"`
	Discovery discoveryPolicy `json:"discovery"`
	Targets   []target        `json:"targets"`
}

type discoveryPolicy struct {
	Roots       []string     `json:"roots"`
	ExcludeDirs []string     `json:"exclude_dirs"`
	Generated   string       `json:"generated"`
	TagMappings []tagMapping `json:"tag_mappings"`
}

type tagMapping struct {
	Expression string   `json:"expression"`
	Tags       []string `json:"tags,omitempty"`
	GOOS       []string `json:"goos,omitempty"`
	GOARCH     []string `json:"goarch,omitempty"`
	Exclude    bool     `json:"exclude,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type target struct {
	Package string   `json:"pkg"`
	Name    string   `json:"target"`
	Tags    []string `json:"tags"`
	GOOS    []string `json:"goos"`
	GOARCH  []string `json:"goarch"`
}

type discoveredTarget struct {
	Package    string
	Name       string
	Expression string
}

type matrix struct {
	Include []target `json:"include"`
}

func targetKey(packageName, name string) string { return packageName + "\x00" + name }

func key(value target) string { return targetKey(value.Package, value.Name) }

func loadManifest(path string) (manifest, error) {
	var result manifest
	data, err := os.ReadFile(path) // #nosec G304 -- path is the explicitly selected repository manifest.
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if result.Version != 1 || len(result.Discovery.Roots) == 0 || result.Discovery.Generated != "exclude" {
		return result, errors.New("manifest requires version 1, discovery roots, and generated=exclude")
	}
	return result, nil
}

func excluded(rel string, policy discoveryPolicy) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	for _, item := range policy.ExcludeDirs {
		item = filepath.ToSlash(filepath.Clean(item))
		if rel == item || strings.HasPrefix(rel, item+"/") || strings.Contains(rel, "/"+item+"/") {
			return true
		}
	}
	return false
}

func buildTags(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//go:build ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "//go:build "))
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
			break
		}
	}
	return ""
}

func isFuzzFunction(decl *ast.FuncDecl) bool {
	if decl.Recv != nil || !strings.HasPrefix(decl.Name.Name, "Fuzz") || decl.Type.Params == nil || len(decl.Type.Params.List) == 0 {
		return false
	}
	star, ok := decl.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := star.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "F" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "testing"
}

func discover(root string, policy discoveryPolicy) ([]discoveredTarget, map[string]bool, error) {
	var targets []discoveredTarget
	seedDirs := map[string]bool{}
	for _, discoveryRoot := range policy.Roots {
		start := filepath.Join(root, discoveryRoot)
		err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if entry.IsDir() && excluded(rel, policy) {
				return filepath.SkipDir
			}
			if entry.IsDir() {
				parent := filepath.ToSlash(filepath.Dir(rel))
				if strings.HasSuffix(parent, "/testdata/fuzz") || parent == "testdata/fuzz" {
					seedDirs[filepath.ToSlash(rel)] = true
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") || excluded(rel, policy) {
				return nil
			}
			data, err := os.ReadFile(path) // #nosec G304,G122 -- trusted repository path returned by this WalkDir call.
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ParseComments)
			if err != nil {
				return err
			}
			if ast.IsGenerated(file) {
				return nil
			}
			packageRel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && isFuzzFunction(function) {
					targets = append(targets, discoveredTarget{Package: "./" + filepath.ToSlash(packageRel), Name: function.Name.Name, Expression: buildTags(data)})
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		return targetKey(targets[i].Package, targets[i].Name) < targetKey(targets[j].Package, targets[j].Name)
	})
	return targets, seedDirs, nil
}

var safeTag = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

var knownGOOS = stringSet("aix android darwin dragonfly freebsd illumos ios js linux netbsd openbsd plan9 solaris wasip1 windows")
var knownGOARCH = stringSet("386 amd64 arm arm64 loong64 mips mips64 mips64le mipsle ppc64 ppc64le riscv64 s390x wasm")

func stringSet(values string) map[string]bool {
	result := map[string]bool{}
	for _, value := range strings.Fields(values) {
		result[value] = true
	}
	return result
}

func sameTags(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validate(root string, policy manifest) ([]target, error) {
	discovered, seedDirs, err := discover(root, policy.Discovery)
	if err != nil {
		return nil, err
	}
	wanted := map[string]target{}
	var problems []string
	mappings := map[string]tagMapping{}
	usedMappings := map[string]bool{}
	for _, mapping := range policy.Discovery.TagMappings {
		hasSelectors := len(mapping.Tags)+len(mapping.GOOS)+len(mapping.GOARCH) > 0
		if mapping.Expression == "" || (mapping.Exclude == hasSelectors) {
			problems = append(problems, "tag mapping must define exactly one of execution selectors or exclude: "+mapping.Expression)
			continue
		}
		if mapping.Exclude && strings.TrimSpace(mapping.Reason) == "" {
			problems = append(problems, "excluded tag mapping requires a reason: "+mapping.Expression)
		}
		seenTags := map[string]bool{}
		for _, tag := range mapping.Tags {
			if !safeTag.MatchString(tag) || seenTags[tag] || knownGOOS[tag] || knownGOARCH[tag] {
				problems = append(problems, "invalid or duplicate mapped build tag: "+tag)
			}
			seenTags[tag] = true
		}
		for _, selector := range []struct {
			name   string
			values []string
			known  map[string]bool
		}{{"GOOS", mapping.GOOS, knownGOOS}, {"GOARCH", mapping.GOARCH, knownGOARCH}} {
			seen := map[string]bool{}
			for _, value := range selector.values {
				if !selector.known[value] || seen[value] {
					problems = append(problems, "invalid or duplicate "+selector.name+" selector: "+value)
				}
				seen[value] = true
			}
		}
		if _, exists := mappings[mapping.Expression]; exists {
			problems = append(problems, "duplicate tag mapping: "+mapping.Expression)
		}
		mappings[mapping.Expression] = mapping
	}
	for _, item := range policy.Targets {
		if item.Package == "" || item.Name == "" || !strings.HasPrefix(item.Package, "./") {
			problems = append(problems, fmt.Sprintf("invalid manifest target: %+v", item))
			continue
		}
		seenTags := map[string]bool{}
		for _, tag := range item.Tags {
			if !safeTag.MatchString(tag) || seenTags[tag] || knownGOOS[tag] || knownGOARCH[tag] {
				problems = append(problems, "invalid or duplicate manifest build tag: "+tag)
			}
			seenTags[tag] = true
		}
		for _, selector := range []struct {
			name   string
			values []string
			known  map[string]bool
		}{{"GOOS", item.GOOS, knownGOOS}, {"GOARCH", item.GOARCH, knownGOARCH}} {
			seen := map[string]bool{}
			for _, value := range selector.values {
				if !selector.known[value] || seen[value] {
					problems = append(problems, "invalid or duplicate manifest "+selector.name+" selector: "+value)
				}
				seen[value] = true
			}
		}
		if _, exists := wanted[key(item)]; exists {
			problems = append(problems, "duplicate manifest target: "+item.Package+" "+item.Name)
		}
		wanted[key(item)] = item
	}
	found := map[string]target{}
	for _, item := range discovered {
		tags := []string{}
		goos := []string{}
		goarch := []string{}
		excludedTarget := false
		if item.Expression != "" {
			mapping, ok := mappings[item.Expression]
			if !ok {
				problems = append(problems, "unmapped build expression: "+item.Package+" "+item.Name+" expression="+item.Expression)
			} else {
				usedMappings[item.Expression] = true
				tags = append(tags, mapping.Tags...)
				goos = append(goos, mapping.GOOS...)
				goarch = append(goarch, mapping.GOARCH...)
				excludedTarget = mapping.Exclude
			}
		}
		seed := strings.TrimPrefix(item.Package, "./") + "/testdata/fuzz/" + item.Name
		entries, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(seed)))
		if readErr != nil || len(entries) == 0 {
			problems = append(problems, "missing or empty fuzz seed corpus: "+seed)
		}
		delete(seedDirs, seed)
		if excludedTarget {
			if _, ok := wanted[targetKey(item.Package, item.Name)]; ok {
				problems = append(problems, "excluded fuzz target must not appear in targets: "+item.Package+" "+item.Name)
			}
			continue
		}
		derived := target{Package: item.Package, Name: item.Name, Tags: tags, GOOS: goos, GOARCH: goarch}
		found[key(derived)] = derived
		manifestItem, ok := wanted[key(derived)]
		if !ok {
			problems = append(problems, "unmanifested fuzz target: "+item.Package+" "+item.Name)
		} else if !sameTags(manifestItem.Tags, tags) || !sameTags(manifestItem.GOOS, goos) || !sameTags(manifestItem.GOARCH, goarch) {
			problems = append(problems, fmt.Sprintf("fuzz target execution selectors differ: %s %s", item.Package, item.Name))
		}
	}
	for itemKey, item := range wanted {
		if _, ok := found[itemKey]; !ok {
			problems = append(problems, "missing fuzz target: "+item.Package+" "+item.Name)
		}
	}
	for expression := range mappings {
		if !usedMappings[expression] {
			problems = append(problems, "stale tag mapping: "+expression)
		}
	}
	for orphan := range seedDirs {
		problems = append(problems, "orphan fuzz seed corpus: "+orphan)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	sort.Slice(policy.Targets, func(i, j int) bool { return key(policy.Targets[i]) < key(policy.Targets[j]) })
	return policy.Targets, nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	manifestPath := flag.String("manifest", "testing/fuzz-targets.json", "manifest path relative to root")
	mode := flag.String("mode", "validate", "validate or matrix")
	flag.Parse()
	policy, err := loadManifest(filepath.Join(*root, *manifestPath))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fuzz manifest:", err)
		os.Exit(1)
	}
	targets, err := validate(*root, policy)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch *mode {
	case "validate":
		fmt.Printf("fuzz manifest: %d targets\n", len(targets))
	case "matrix":
		encoded, err := json.Marshal(matrix{Include: targets})
		if err != nil {
			panic(err)
		}
		fmt.Println(string(encoded))
	default:
		fmt.Fprintln(os.Stderr, "fuzz manifest: mode must be validate or matrix")
		os.Exit(2)
	}
}
