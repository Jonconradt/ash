package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestBuildPlanAllModeMarksChangedWhenPrecleanRewritesInvalidPersistenceEID(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "invalid.go")

	prefix := "package main\n\nfunc f() {\n\tlogPersistenceError(\""
	suffix := "\", \"msg\", nil)\n}\n"
	invalid := []byte{0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0xf0, 0xf1}
	src := append([]byte(prefix), invalid...)
	src = append(src, []byte(suffix)...)
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, true)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected non-nil plan")
	}
	if !plan.changed {
		t.Fatalf("expected plan.changed=true when preclean rewrites invalid EID")
	}

	changed, err := writePlan(plan)
	if err != nil {
		t.Fatalf("writePlan: %v", err)
	}
	if !changed {
		t.Fatalf("expected writePlan to persist preclean rewrite")
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.Contains(string(out), "\\xff") {
		t.Fatalf("expected invalid escaped bytes to be replaced")
	}
}

func TestResolveFilesAllModeSkipsTempAndInjectorSource(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	mustWrite := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	mustWrite("ok.go", "package main\n")
	mustWrite(".!12345!bad.go", "package main\n")
	mustWrite("scripts/eid-injector/main.go", "package main\n")

	files, err := resolveFiles(true, nil)
	if err != nil {
		t.Fatalf("resolveFiles: %v", err)
	}

	joined := strings.Join(files, "\n")
	if !strings.Contains(joined, "ok.go") {
		t.Fatalf("expected ok.go to be included; got: %v", files)
	}
	if strings.Contains(joined, ".!12345!bad.go") {
		t.Fatalf("did not expect temporary .! file in scan set")
	}
	if strings.Contains(joined, "scripts/eid-injector/main.go") {
		t.Fatalf("did not expect injector source in scan set")
	}
}

func TestResolveFilesRejectsPathTraversalOutsideRoot(t *testing.T) {
	rootDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(rootDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	_, err = resolveFiles(false, []string{outsideFile})
	if err == nil {
		t.Fatalf("expected traversal path rejection")
	}
}

func TestResolveFilesRejectsSymlinkEscapingRoot(t *testing.T) {
	rootDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(rootDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := os.Symlink(outsideFile, filepath.Join(rootDir, "linked.go")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err = resolveFiles(false, []string{"linked.go"})
	if err == nil {
		t.Fatalf("expected symlink escape rejection")
	}
}

func TestBuildPlanReturnsErrNoChangesForUnmodifiedNonAllMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plain.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if !errors.Is(err, errNoChanges) {
		t.Fatalf("expected errNoChanges, got plan=%v err=%v", plan, err)
	}
}

func TestBuildPlanNormalizesInvalidSplitEllipsisEID(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ellipsis_split.go")
	src := `package main

import "log/slog"

func f(message string, logAttrs []any) {
	slog.Error(message, logAttrs, "EID", "JlqS7vEA"...)
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan == nil || !plan.changed {
		t.Fatalf("expected changed plan for split ellipsis EID")
	}
	if _, err := writePlan(plan); err != nil {
		t.Fatalf("writePlan: %v", err)
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(out)
	re := regexp.MustCompile(`slog\.Error\(message,\s*append\(logAttrs,\s*"EID",\s*"JlqS7vEA"\)\.\.\.,?\s*\)`)
	if !re.MatchString(got) {
		t.Fatalf("expected normalized append variadic slog call, got:\n%s", got)
	}
}

func TestBuildPlanPreservesVariadicAttrsWhenInjectingEID(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ellipsis_attrs.go")
	src := `package main

import "log/slog"

func f(message string, attrs []any) {
	slog.Error(message, attrs...)
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan == nil || !plan.changed {
		t.Fatalf("expected changed plan when injecting EID for variadic attrs")
	}
	if _, err := writePlan(plan); err != nil {
		t.Fatalf("writePlan: %v", err)
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(out)
	re := regexp.MustCompile(`slog\.Error\(message,\s*append\(attrs,\s*"EID",\s*"[A-Za-z0-9]{8}"\)\.\.\.,?\s*\)`)
	if !re.MatchString(got) {
		t.Fatalf("expected variadic attrs to stay spread with injected EID, got:\n%s", got)
	}
}

func TestBuildPlanDoesNotInjectDuplicateStaticEIDWhenSpreadAppendAlreadyHasDynamicEID(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ellipsis_dynamic_eid.go")
	src := `package main

import "log/slog"

func f(message, eid string, err error, attrs []any) {
	slog.Error(message, append([]any{"EID", eid, "error", err}, attrs...)...)
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if !errors.Is(err, errNoChanges) {
		t.Fatalf("expected errNoChanges for existing dynamic EID append spread; plan=%v err=%v", plan, err)
	}
}

func TestBuildPlanInjectsEIDForToolCommandResultErrorWithExplicitOKFalse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool_result_explicit_false.go")
	src := `package main

func f() toolCommandResult {
	return toolCommandResult{OK: false, Error: "bad input"}
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan == nil || !plan.changed {
		t.Fatalf("expected changed plan")
	}
	if _, err := writePlan(plan); err != nil {
		t.Fatalf("writePlan: %v", err)
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !regexp.MustCompile(`EID:\s*"[A-Za-z0-9]{8}"`).Match(out) {
		t.Fatalf("expected EID field to be injected, got:\n%s", string(out))
	}
}

func TestBuildPlanInjectsEIDForToolCommandResultErrorWithoutOKField(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool_result_missing_ok.go")
	src := `package main

func f() toolCommandResult {
	return toolCommandResult{Command: "x", Error: "bad input"}
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan == nil || !plan.changed {
		t.Fatalf("expected changed plan")
	}
	if _, err := writePlan(plan); err != nil {
		t.Fatalf("writePlan: %v", err)
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !regexp.MustCompile(`EID:\s*"[A-Za-z0-9]{8}"`).Match(out) {
		t.Fatalf("expected EID field to be injected, got:\n%s", string(out))
	}
}

func TestBuildPlanSkipsToolCommandResultWithOKTrue(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool_result_ok_true.go")
	src := `package main

func f() toolCommandResult {
	return toolCommandResult{OK: true, Error: "not an error path"}
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if !errors.Is(err, errNoChanges) {
		t.Fatalf("expected errNoChanges, got plan=%v err=%v", plan, err)
	}
}

func TestBuildPlanSkipsToolCommandResultWithExistingEID(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool_result_existing_eid.go")
	src := `package main

func f() toolCommandResult {
	return toolCommandResult{OK: false, Error: "bad input", EID: "AB12CD34"}
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if !errors.Is(err, errNoChanges) {
		t.Fatalf("expected errNoChanges, got plan=%v err=%v", plan, err)
	}
}

func TestBuildPlanInjectsEIDForToolCommandResultDynamicErrorExpr(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool_result_dynamic_error.go")
	src := `package main

func f(err error) toolCommandResult {
	return toolCommandResult{OK: false, Error: err.Error()}
}
`
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	plan, err := buildPlan(file, false)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan == nil || !plan.changed {
		t.Fatalf("expected changed plan")
	}
	if _, err := writePlan(plan); err != nil {
		t.Fatalf("writePlan: %v", err)
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !regexp.MustCompile(`EID:\s*"[A-Za-z0-9]{8}"`).Match(out) {
		t.Fatalf("expected EID field to be injected, got:\n%s", string(out))
	}
}

func TestDedupeLiteralEIDsWithToolCommandResultInsertions(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "tool_result_a.go")
	file2 := filepath.Join(dir, "tool_result_b.go")
	src := `package main

func f() toolCommandResult {
	return toolCommandResult{OK: false, Error: "bad input"}
}
`
	if err := os.WriteFile(file1, []byte(src), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte(src), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	plan1, err := buildPlan(file1, false)
	if err != nil {
		t.Fatalf("buildPlan file1: %v", err)
	}
	plan2, err := buildPlan(file2, false)
	if err != nil {
		t.Fatalf("buildPlan file2: %v", err)
	}
	if len(plan1.eids) != 1 || len(plan2.eids) != 1 {
		t.Fatalf("expected one injected EID literal per plan")
	}

	plan1.eids[0].Value = "\"DUPL0001\""
	plan2.eids[0].Value = "\"DUPL0001\""
	dedupeLiteralEIDs([]*filePlan{plan1, plan2})

	v1, ok := parseStringLiteral(plan1.eids[0])
	if !ok || !isValidEIDValue(v1) {
		t.Fatalf("expected plan1 EID to remain valid, got %q", plan1.eids[0].Value)
	}
	v2, ok := parseStringLiteral(plan2.eids[0])
	if !ok || !isValidEIDValue(v2) {
		t.Fatalf("expected plan2 EID to remain valid, got %q", plan2.eids[0].Value)
	}
	if v1 == v2 {
		t.Fatalf("expected dedupe to produce unique values, both were %q", v1)
	}
}
