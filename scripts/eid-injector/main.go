package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

var levelNames = map[string]struct{}{
	"Info":  {},
	"Error": {},
	"Warn":  {},
	"Debug": {},
}

const canonicalEIDKey = "EID"
const toolCommandResultTypeName = "toolCommandResult"

var errNoChanges = errors.New("no changes")

type filePlan struct {
	filename string
	src      []byte
	file     *ast.File
	fset     *token.FileSet
	changed  bool
	eids     []*ast.BasicLit
}

func writeEIDLogf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.TrimRight(message, "\r\n")
	_, _ = fmt.Fprintf(w, "%s [EID=%s]\n", message, mustRandAlphaNum(8))
}

func main() {
	allMode := flag.Bool("all", false, "scan all .go files recursively from current directory")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "Usage: eid-injector [--all] [files.go ...] [EID=rn8kf5vA]")
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "Ensures each slog log call ends with \"EID\", <code>. [EID=pdMQ4jAS]")
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "In --all mode, it also validates/fixes logPersistenceError EIDs and de-duplicates literal EIDs globally. [EID=sIh9akd6]")
	}
	flag.Parse()

	files, err := resolveFiles(*allMode, flag.Args())
	if err != nil {
		writeEIDLogf(os.Stderr, "eid-injector: %v", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		return
	}

	plans := make([]*filePlan, 0, len(files))
	for _, file := range files {
		plan, err := buildPlan(file, *allMode)
		if err != nil {
			if errors.Is(err, errNoChanges) {
				continue
			}
			writeEIDLogf(os.Stderr, "eid-injector: %s: %v", file, err)
			continue
		}
		if plan != nil {
			plans = append(plans, plan)
		}
	}

	if *allMode {
		dedupeLiteralEIDs(plans)
	}

	wroteAny := false
	for _, plan := range plans {
		changed, err := writePlan(plan)
		if err != nil {
			writeEIDLogf(os.Stderr, "eid-injector: %s: %v", plan.filename, err)
			continue
		}
		if changed {
			wroteAny = true
		}
	}

	if wroteAny {
		writeEIDLogf(os.Stdout, "eid-injector: injected/standardized EIDs where needed.")
	}
}

func resolveFiles(allMode bool, args []string) ([]string, error) {
	rootAbs, err := filepath.Abs(".")
	if err != nil {
		return nil, err
	}
	rootEval, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		rootEval = rootAbs
	}

	//nolint:nestif // Repository walk filters are explicit and bounded.
	if allMode {
		files := make([]string, 0, 256)
		err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := filepath.Base(path)
				if base == ".git" || base == "vendor" || base == "node_modules" || base == "bin" || base == "releases" {
					return filepath.SkipDir
				}
				return nil
			}
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".!") {
				return nil
			}
			if filepath.Clean(path) == filepath.Clean("scripts/eid-injector/main.go") {
				return nil
			}
			if strings.HasSuffix(path, ".go") {
				safePath, err := constrainToRoot(path, rootEval)
				if err != nil {
					return err
				}
				files = append(files, safePath)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return files, nil
	}

	files := make([]string, 0, len(args))
	for _, file := range args {
		if strings.HasSuffix(file, ".go") {
			safePath, err := constrainToRoot(file, rootEval)
			if err != nil {
				return nil, err
			}
			files = append(files, safePath)
		}
	}
	return files, nil
}

func constrainToRoot(path string, root string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	rel, err := filepath.Rel(root, resolvedPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes repository root: %s", path)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("expected file, got directory: %s", path)
	}

	return resolvedPath, nil
}

func resolveInputFilePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty path")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = absPath
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("expected file, got directory: %s", path)
	}

	return resolvedPath, nil
}

func buildPlan(filename string, allMode bool) (*filePlan, error) {
	safePath, err := resolveInputFilePath(filename)
	if err != nil {
		return nil, err
	}

	// #nosec G304 -- safePath has already been resolved and validated before use
	originalSrc, err := os.ReadFile(safePath)
	if err != nil {
		return nil, err
	}
	src := append([]byte(nil), originalSrc...)
	precleanChanged := false

	if allMode {
		cleaned, changed := precleanInvalidLogPersistenceIDs(src)
		if changed {
			src = cleaned
			precleanChanged = true
		}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	hasSlog := false
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "log/slog" {
			hasSlog = true
			break
		}
	}

	plan := &filePlan{filename: safePath, src: originalSrc, file: f, fset: fset, changed: precleanChanged}

	//nolint:nestif // AST traversal conditions remain explicit for safety.
	if hasSlog {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			xid, ok := sel.X.(*ast.Ident)
			if !ok || xid == nil || xid.Name != "slog" {
				return true
			}
			if _, ok := levelNames[sel.Sel.Name]; !ok {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			changed, lit := normalizeSlogCall(call)
			if changed {
				plan.changed = true
			}
			if lit != nil {
				plan.eids = append(plan.eids, lit)
			}
			return true
		})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		changed, lit := normalizeFmtLogCall(call)
		if changed {
			plan.changed = true
		}
		if lit != nil {
			plan.eids = append(plan.eids, lit)
		}
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}

		changed, eidLit := normalizeToolCommandResultLiteral(lit)
		if changed {
			plan.changed = true
		}
		if eidLit != nil {
			plan.eids = append(plan.eids, eidLit)
		}
		return true
	})

	if allMode {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "logPersistenceError" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, ok := parseStringLiteral(lit)
			if !ok || !isValidEIDValue(value) {
				lit.Value = strconv.Quote(mustRandAlphaNum(8))
				plan.changed = true
			}
			plan.eids = append(plan.eids, lit)
			return true
		})
	}

	if !plan.changed && !allMode {
		return nil, errNoChanges
	}
	return plan, nil
}

func normalizeFmtLogCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	if len(call.Args) == 0 {
		return false, nil
	}

	if ident, ok := call.Fun.(*ast.Ident); ok {
		switch ident.Name {
		case "writeLogf":
			return normalizeWriteLogfCall(call)
		case "writeLogLine":
			return normalizeWriteLogLineCall(call)
		}
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false, nil
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "fmt" {
		return false, nil
	}

	switch sel.Sel.Name {
	case "Fprintf":
		return normalizeFmtFprintfCall(call)
	case "Fprint", "Fprintln":
		return normalizeFmtFprintLikeCall(call)
	default:
		return false, nil
	}
}

func normalizeWriteLogfCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	return normalizeStringLiteralCall(call, 1)
}

func normalizeWriteLogLineCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	return normalizeStringLiteralCall(call, 1)
}

func normalizeStringLiteralCall(call *ast.CallExpr, index int) (bool, *ast.BasicLit) {
	if len(call.Args) <= index {
		return false, nil
	}
	lit, ok := call.Args[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false, nil
	}
	value, ok := parseStringLiteral(lit)
	if !ok || strings.Contains(value, "EID=") {
		return false, nil
	}
	newValue := value + " [EID=" + mustRandAlphaNum(8) + "]"
	lit.Value = strconv.Quote(newValue)
	return true, nil
}

func normalizeFmtFprintfCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	return normalizeFmtStringLiteralCall(call, 1)
}

func normalizeFmtFprintLikeCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	return normalizeFmtStringLiteralCall(call, 1)
}

func normalizeFmtStringLiteralCall(call *ast.CallExpr, index int) (bool, *ast.BasicLit) {
	if len(call.Args) <= index {
		return false, nil
	}
	lit, ok := call.Args[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false, nil
	}
	value, ok := parseStringLiteral(lit)
	if !ok || strings.Contains(value, "EID=") {
		return false, nil
	}
	newValue := value + " [EID=" + mustRandAlphaNum(8) + "]"
	lit.Value = strconv.Quote(newValue)
	return true, nil
}

func normalizeSlogCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	if call.Ellipsis.IsValid() && len(call.Args) >= 2 {
		return normalizeSlogEllipsisCall(call)
	}

	updated := false
	type occurrence struct {
		kind  string
		index int
		value ast.Expr
	}
	occurrences := make([]occurrence, 0, 2)

	for i := 1; i+1 < len(call.Args); i++ {
		lit, ok := call.Args[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		key, ok := parseStringLiteral(lit)
		if !ok || !strings.EqualFold(key, canonicalEIDKey) {
			continue
		}
		occurrences = append(occurrences, occurrence{kind: "pair", index: i, value: call.Args[i+1]})
	}

	for i := 1; i < len(call.Args); i++ {
		ce, ok := call.Args[i].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		xid, ok := sel.X.(*ast.Ident)
		if !ok || xid.Name != "slog" || sel.Sel.Name != "String" || len(ce.Args) < 2 {
			continue
		}
		keyLit, ok := ce.Args[0].(*ast.BasicLit)
		if !ok || keyLit.Kind != token.STRING {
			continue
		}
		key, ok := parseStringLiteral(keyLit)
		if !ok || !strings.EqualFold(key, canonicalEIDKey) {
			continue
		}
		occurrences = append(occurrences, occurrence{kind: "attr", index: i, value: ce.Args[1]})
	}

	if trailingUpdated, trailingLit, handled := normalizeTrailingEIDPair(call.Args); handled {
		if trailingUpdated {
			updated = true
		}
		return updated, trailingLit
	}

	var eidValue ast.Expr
	if len(occurrences) > 0 {
		eidValue = occurrences[0].value
	}

	for i := len(occurrences) - 1; i >= 0; i-- {
		occ := occurrences[i]
		switch occ.kind {
		case "pair":
			call.Args = append(call.Args[:occ.index], call.Args[occ.index+2:]...)
		case "attr":
			call.Args = append(call.Args[:occ.index], call.Args[occ.index+1:]...)
		}
	}

	var outLit *ast.BasicLit
	if eidValue == nil {
		outLit = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(mustRandAlphaNum(8))}
		eidValue = outLit
	} else if lit, ok := eidValue.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		value, ok := parseStringLiteral(lit)
		if !ok || !isValidEIDValue(value) {
			lit.Value = strconv.Quote(mustRandAlphaNum(8))
		}
		outLit = lit
	}

	eidKey := &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(canonicalEIDKey)}
	call.Args = append(call.Args, eidKey, eidValue)
	updated = true
	return updated, outLit
}

func normalizeToolCommandResultLiteral(lit *ast.CompositeLit) (bool, *ast.BasicLit) {
	if !isToolCommandResultLiteral(lit) {
		return false, nil
	}

	fields, ok := extractKeyedStructLiteralFields(lit)
	if !ok {
		return false, nil
	}

	if _, hasEID := fields[canonicalEIDKey]; hasEID {
		return false, nil
	}

	errorValue, hasError := fields["Error"]
	if !hasError || isNilLiteral(errorValue) {
		return false, nil
	}

	if okValue, hasOK := fields["OK"]; hasOK {
		okBool, known := boolLiteralValue(okValue)
		if !known || okBool {
			return false, nil
		}
	}

	eidLit := &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(mustRandAlphaNum(8))}
	lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: ast.NewIdent(canonicalEIDKey), Value: eidLit})
	return true, eidLit
}

func isToolCommandResultLiteral(lit *ast.CompositeLit) bool {
	if lit == nil || lit.Type == nil {
		return false
	}

	switch t := lit.Type.(type) {
	case *ast.Ident:
		return t.Name == toolCommandResultTypeName
	case *ast.SelectorExpr:
		return t.Sel != nil && t.Sel.Name == toolCommandResultTypeName
	default:
		return false
	}
}

func extractKeyedStructLiteralFields(lit *ast.CompositeLit) (map[string]ast.Expr, bool) {
	fields := make(map[string]ast.Expr, len(lit.Elts))
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			return nil, false
		}
		fields[key.Name] = kv.Value
	}
	return fields, true
}

func boolLiteralValue(expr ast.Expr) (bool, bool) {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return false, false
	}
	switch id.Name {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func isNilLiteral(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "nil"
}

func normalizeTrailingEIDPair(args []ast.Expr) (bool, *ast.BasicLit, bool) {
	if len(args) < 3 {
		return false, nil, false
	}
	trailing, ok := args[len(args)-2].(*ast.BasicLit)
	if !ok || trailing.Kind != token.STRING {
		return false, nil, false
	}
	key, ok := parseStringLiteral(trailing)
	if !ok || !strings.EqualFold(key, canonicalEIDKey) {
		return false, nil, false
	}
	updated := false
	if key != canonicalEIDKey {
		trailing.Value = strconv.Quote(canonicalEIDKey)
		updated = true
	}
	if vlit, ok := args[len(args)-1].(*ast.BasicLit); ok && vlit.Kind == token.STRING {
		value, parsed := parseStringLiteral(vlit)
		if !parsed || !isValidEIDValue(value) {
			vlit.Value = strconv.Quote(mustRandAlphaNum(8))
			updated = true
		}
		return updated, vlit, true
	}
	return updated, nil, true
}

func normalizeSlogEllipsisCall(call *ast.CallExpr) (bool, *ast.BasicLit) {
	message := call.Args[0]
	prefix := append([]ast.Expr(nil), call.Args[1:len(call.Args)-1]...)
	spread := call.Args[len(call.Args)-1]
	useSpread := true

	var eidValue ast.Expr

	if len(prefix) >= 1 {
		if keyLit, ok := prefix[len(prefix)-1].(*ast.BasicLit); ok && keyLit.Kind == token.STRING {
			if key, ok := parseStringLiteral(keyLit); ok && strings.EqualFold(key, canonicalEIDKey) {
				prefix = prefix[:len(prefix)-1]
				eidValue = spread
				useSpread = false
			}
		}
	}

	if eidValue == nil && len(prefix) >= 2 {
		if keyLit, ok := prefix[len(prefix)-2].(*ast.BasicLit); ok && keyLit.Kind == token.STRING {
			if key, ok := parseStringLiteral(keyLit); ok && strings.EqualFold(key, canonicalEIDKey) {
				eidValue = prefix[len(prefix)-1]
				prefix = prefix[:len(prefix)-2]
			}
		}
	}

	// Keep existing variadic attrs unchanged when they already embed an EID key
	// (for example append([]any{"EID", eid, ...}, attrs...)...).
	if eidValue == nil && len(prefix) == 0 && expressionContainsCanonicalEIDKey(spread) {
		return false, nil
	}

	var outLit *ast.BasicLit
	if eidValue == nil {
		outLit = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(mustRandAlphaNum(8))}
		eidValue = outLit
	} else if lit, ok := eidValue.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		value, ok := parseStringLiteral(lit)
		if !ok || !isValidEIDValue(value) {
			lit.Value = strconv.Quote(mustRandAlphaNum(8))
		}
		outLit = lit
	}

	eidKey := &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(canonicalEIDKey)}

	if !useSpread {
		if len(prefix) == 0 {
			call.Args = []ast.Expr{message, eidKey, eidValue}
			call.Ellipsis = token.NoPos
			return true, outLit
		}

		base := prefix[0]
		if len(prefix) > 1 {
			base = &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("any")}, Elts: prefix}
		}

		merged := &ast.CallExpr{
			Fun:  ast.NewIdent("append"),
			Args: []ast.Expr{base, eidKey, eidValue},
		}

		call.Args = []ast.Expr{message, merged}
		call.Ellipsis = token.Pos(1)
		return true, outLit
	}

	spreadBase := spread
	if len(prefix) > 0 {
		spreadBase = &ast.CallExpr{
			Fun: ast.NewIdent("append"),
			Args: []ast.Expr{
				&ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("any")}, Elts: prefix},
				spread,
			},
			Ellipsis: token.Pos(1),
		}
	}

	merged := &ast.CallExpr{
		Fun:  ast.NewIdent("append"),
		Args: []ast.Expr{spreadBase, eidKey, eidValue},
	}

	call.Args = []ast.Expr{message, merged}
	call.Ellipsis = token.Pos(1)
	return true, outLit
}

//nolint:gocognit // AST expression scanning intentionally keeps explicit recursion cases.
func expressionContainsCanonicalEIDKey(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return false
		}
		value, ok := parseStringLiteral(e)
		return ok && strings.EqualFold(value, canonicalEIDKey)
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			if expressionContainsCanonicalEIDKey(elt) {
				return true
			}
		}
		return false
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if expressionContainsCanonicalEIDKey(arg) {
				return true
			}
		}
		return false
	case *ast.KeyValueExpr:
		return expressionContainsCanonicalEIDKey(e.Key) || expressionContainsCanonicalEIDKey(e.Value)
	case *ast.ParenExpr:
		return expressionContainsCanonicalEIDKey(e.X)
	case *ast.UnaryExpr:
		return expressionContainsCanonicalEIDKey(e.X)
	default:
		return false
	}
}

func dedupeLiteralEIDs(plans []*filePlan) {
	used := map[string]struct{}{}
	occurrences := map[string][]struct {
		plan *filePlan
		lit  *ast.BasicLit
	}{}

	for _, plan := range plans {
		for _, lit := range plan.eids {
			value, ok := parseStringLiteral(lit)
			if !ok || !isValidEIDValue(value) {
				continue
			}
			occurrences[value] = append(occurrences[value], struct {
				plan *filePlan
				lit  *ast.BasicLit
			}{plan: plan, lit: lit})
			used[value] = struct{}{}
		}
	}

	for _, entries := range occurrences {
		if len(entries) <= 1 {
			continue
		}
		for i := 1; i < len(entries); i++ {
			next := mustRandAlphaNum(8)
			for {
				if _, exists := used[next]; !exists {
					break
				}
				next = mustRandAlphaNum(8)
			}
			used[next] = struct{}{}
			entries[i].lit.Value = strconv.Quote(next)
			entries[i].plan.changed = true
		}
	}
}

func writePlan(plan *filePlan) (bool, error) {
	if !plan.changed {
		return false, nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, plan.fset, plan.file); err != nil {
		return false, err
	}
	newSrc := buf.Bytes()
	if bytes.Equal(plan.src, newSrc) {
		return false, nil
	}
	info, _ := os.Stat(plan.filename)
	mode := os.FileMode(0644)
	if info != nil {
		mode = info.Mode()
	}
	if err := writeFileAtomic(plan.filename, newSrc, mode); err != nil {
		return false, err
	}
	return true, nil
}

func precleanInvalidLogPersistenceIDs(src []byte) ([]byte, bool) {
	needle := []byte(`logPersistenceError("`)
	out := append([]byte(nil), src...)
	changed := false
	start := 0
	for {
		idx := bytes.Index(out[start:], needle)
		if idx < 0 {
			break
		}
		idx += start + len(needle)
		end := idx
		for end < len(out) {
			if out[end] == '"' && (end == idx || out[end-1] != '\\') {
				break
			}
			end++
		}
		if end >= len(out) {
			break
		}
		candidate := string(out[idx:end])
		if !isValidEIDValue(candidate) {
			repl := []byte(mustRandAlphaNum(8))
			out = append(out[:idx], append(repl, out[end:]...)...)
			end = idx + len(repl)
			changed = true
		}
		start = end + 1
	}
	return out, changed
}

func parseStringLiteral(lit *ast.BasicLit) (string, bool) {
	if lit == nil || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func isValidEIDValue(value string) bool {
	if !utf8.ValidString(value) || len(value) != 8 {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			continue
		}
		return false
	}
	return true
}

func writeFileAtomic(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	f, err := os.CreateTemp(dir, ".eidtmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	//nolint:gosec // tmp path is generated by os.CreateTemp in target directory.
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	//nolint:gosec // rename is scoped to the same trusted workspace directory.
	return os.Rename(tmp, filename)
}

var alphaNum = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")

func mustRandAlphaNum(n int) string {
	b := make([]rune, n)
	for i := 0; i < n; i++ {
		idx := cryptoRandInt(len(alphaNum))
		b[i] = alphaNum[idx]
	}
	return string(b)
}

func cryptoRandInt(max int) int {
	if max <= 0 {
		return 0
	}
	m := big.NewInt(int64(max))
	v, err := rand.Int(rand.Reader, m)
	if err != nil {
		return 0
	}
	return int(v.Int64())
}
