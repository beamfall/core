package conformance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type destructiveAuditOwner string

const (
	graphAuditOwner destructiveAuditOwner = "graph"
	storeAuditOwner destructiveAuditOwner = "store"
)

type destructiveAuditRequirement struct {
	Action              string
	Owner               destructiveAuditOwner
	StoreAuditFunctions []string
}

func graphAudit(action string) destructiveAuditRequirement {
	return destructiveAuditRequirement{Action: action, Owner: graphAuditOwner}
}

func storeAudit(action string, functions ...string) destructiveAuditRequirement {
	return destructiveAuditRequirement{Action: action, Owner: storeAuditOwner, StoreAuditFunctions: functions}
}

// destructiveGraphStoreMethods classifies every destructive Store primitive
// reached from Graph, naming both its audit owner and required action. Ordinary
// non-destructive Store calls live in knownNonSeamGraphStoreMethods; destructive
// names must never be hidden there.
var destructiveGraphStoreMethods = map[string]destructiveAuditRequirement{
	"BatchRenameManagedPaths":       graphAudit("ActivityManagedFileMove"),
	"DeleteChannel":                 graphAudit("ActivityChannelDelete"),
	"DeleteLibrary":                 graphAudit("ActivityLibraryDelete"),
	"DeleteManagedPath":             graphAudit("ActivityManagedFileDelete"),
	"DeleteMarker":                  graphAudit("ActivityMarkerDelete"),
	"DeleteMyEngagementAudited":     storeAudit("ActivityMyDataDelete", "deleteProfileMediaAuditedLocked", "resumeProfileDeleteStateLocked"),
	"DeleteMyPackTokens":            graphAudit("ActivityOfflinePackTokensDelete"),
	"DeleteProfileFootprintAudited": storeAudit("ActivityOperatorDataDelete", "resumeProfileDeleteStateLocked"),
	"DeleteRetentionRule":           graphAudit("ActivityRetentionRuleDelete"),
	"DeleteSavedFilter":             graphAudit("ActivitySavedFilterDelete"),
	"DeleteSavedStation":            graphAudit("ActivitySavedStationDelete"),
	"DeleteScrobbleLink":            graphAudit("ActivityScrobbleUnlink"),
	"DeleteSmartCollection":         graphAudit("ActivitySmartCollectionDelete"),
	"DeleteTag":                     graphAudit("ActivityTagDelete"),
	"MergeApply":                    graphAudit("ActivityMediaMerge"),
	"MergeEntities":                 graphAudit("ActivityEntityMerge"),
	"MoveManagedPath":               graphAudit("ActivityManagedFileMove"),
	"OrganizeApply":                 graphAudit("ActivityMediaOrganize"),
	"PurgePluginData":               graphAudit("ActivityPluginUninstall"),
	"PurgeTrashedBefore":            storeAudit("ActivityTrashPurge", "PurgeTrashedBefore"),
	"RenameManagedPath":             graphAudit("ActivityManagedFileMove"),
	"RestoreManagedPath":            graphAudit("ActivityManagedFileRestore"),
	"RestoreMedia":                  graphAudit("ActivityMediaRestore"),
	"TrashCollection":               graphAudit("ActivityCollectionTrash"),
	"TrashMedia":                    graphAudit("ActivityMediaTrash"),
	"UpsertArtworkCandidate":        graphAudit("ActivityArtworkPin"),
}

type destructiveAuditFinding struct {
	File           string
	Function       string
	StoreMethod    string
	RequiredAction string
	AuditOwner     destructiveAuditOwner
	Calls          int
	Audits         int
}

type storeAuditOwnerFinding struct {
	StoreMethod    string
	Function       string
	RequiredAction string
	Reason         string
}

type destructiveCallSite struct {
	Call           *ast.CallExpr
	Method         string
	Result         string
	Error          string
	RequiredAction string
	AuditOwner     destructiveAuditOwner
}

func TestDestructiveGraphPathsUseAuditedMutationSeam(t *testing.T) {
	for method := range knownNonSeamGraphStoreMethods {
		if isDestructiveStoreMethodName(method) {
			t.Errorf("destructive store.%s is hidden in the non-seam inventory", method)
		}
	}
	graphDir := requireRoot(t, filepath.Join("internal", "graph"))
	findings, observed, scanned, err := scanDestructiveGraphAudit(graphDir)
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("destructive audit completeness gate scanned zero Graph production files")
	}
	for _, finding := range findings {
		if finding.RequiredAction == "" {
			t.Errorf("%s: %s calls unclassified destructive store.%s",
				finding.File, finding.Function, finding.StoreMethod)
			continue
		}
		t.Errorf("%s: %s calls store.%s %d time(s) but logs store.%s %d time(s)",
			finding.File, finding.Function, finding.StoreMethod, finding.Calls,
			finding.RequiredAction, finding.Audits)
	}
	storeDir := requireRoot(t, filepath.Join("internal", "store"))
	storeFindings, err := scanStoreAuditOwners(storeDir, observed)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range storeFindings {
		t.Errorf("store.%s requires store-owned %s in %s: %s",
			finding.StoreMethod, finding.RequiredAction, finding.Function, finding.Reason)
	}

	methods := make([]string, 0, len(destructiveGraphStoreMethods))
	for method := range destructiveGraphStoreMethods {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		if observed[method] == 0 {
			t.Errorf("destructive audit completeness inventory is stale: store.%s has no Graph call site", method)
		}
	}
}

func isDestructiveStoreMethodName(method string) bool {
	return hasMethodVerbPrefix(method, "Delete") ||
		hasMethodVerbPrefix(method, "Purge") ||
		hasMethodVerbPrefix(method, "Trash")
}

func hasMethodVerbPrefix(method, verb string) bool {
	if !strings.HasPrefix(method, verb) {
		return false
	}
	return len(method) == len(verb) ||
		method[len(verb)] >= 'A' && method[len(verb)] <= 'Z'
}

func scanDestructiveGraphAudit(dir string) ([]destructiveAuditFinding, map[string]int, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read Graph source directory: %w", err)
	}
	var findings []destructiveAuditFinding
	observed := map[string]int{}
	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		//nolint:gosec // test fixture: path is a filtered Go file in the selected source directory.
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, scanned, fmt.Errorf("read %s: %w", path, err)
		}
		fileFindings, fileObserved, err := inspectDestructiveGraphAudit(path, source)
		if err != nil {
			return nil, nil, scanned, err
		}
		findings = append(findings, fileFindings...)
		for method, count := range fileObserved {
			observed[method] += count
		}
		scanned++
	}
	return findings, observed, scanned, nil
}

func inspectDestructiveGraphAudit(path string, source []byte) ([]destructiveAuditFinding, map[string]int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var findings []destructiveAuditFinding
	observed := map[string]int{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		calls := collectGraphStoreCalls(function)
		for _, call := range calls {
			if _, known := knownNonSeamGraphStoreMethods[call.Method]; known {
				continue
			}
			observed[call.Method]++
			if call.AuditOwner == storeAuditOwner {
				continue
			}
			if call.RequiredAction != "" && hasFollowingActivity(function.Body, call, calls) {
				continue
			}
			findings = append(findings, destructiveAuditFinding{
				File:           filepath.ToSlash(path),
				Function:       function.Name.Name,
				StoreMethod:    call.Method,
				RequiredAction: call.RequiredAction,
				AuditOwner:     call.AuditOwner,
				Calls:          1,
			})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Function != findings[j].Function {
			return findings[i].Function < findings[j].Function
		}
		return findings[i].StoreMethod < findings[j].StoreMethod
	})
	return findings, observed, nil
}

func scanStoreAuditOwners(dir string, observed map[string]int) ([]storeAuditOwnerFinding, error) {
	functions, err := parseStoreFunctions(dir)
	if err != nil {
		return nil, err
	}
	var findings []storeAuditOwnerFinding
	for method, requirement := range destructiveGraphStoreMethods {
		if observed[method] == 0 || requirement.Owner != storeAuditOwner {
			continue
		}
		for _, functionName := range requirement.StoreAuditFunctions {
			function := functions[functionName]
			switch {
			case function == nil:
				findings = append(findings, storeAuditOwnerFinding{
					StoreMethod:    method,
					Function:       functionName,
					RequiredAction: requirement.Action,
					Reason:         "audit owner function not found",
				})
			case !functionChecksNamedAudit(function, "appendActivitySQL"):
				findings = append(findings, storeAuditOwnerFinding{
					StoreMethod:    method,
					Function:       functionName,
					RequiredAction: requirement.Action,
					Reason:         "durable activity append is missing or its error can reach success",
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].StoreMethod != findings[j].StoreMethod {
			return findings[i].StoreMethod < findings[j].StoreMethod
		}
		return findings[i].Function < findings[j].Function
	})
	return findings, nil
}

func parseStoreFunctions(dir string) (map[string]*ast.FuncDecl, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read Store source directory: %w", err)
	}
	functions := map[string]*ast.FuncDecl{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		source, err := os.ReadFile(path) //nolint:gosec // filtered production Store source.
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Body != nil {
				functions[function.Name.Name] = function
			}
		}
	}
	return functions, nil
}

func functionChecksNamedAudit(function *ast.FuncDecl, name string) bool {
	found := 0
	valid := true
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || calledFunctionName(call) != name {
			return true
		}
		found++
		statement := containingAuditControlStatement(function.Body, call)
		if statement == nil || !statementChecksAuditCall(statement, name, "") {
			valid = false
		}
		return false
	})
	return found > 0 && valid
}

func containingAuditControlStatement(body *ast.BlockStmt, target ast.Node) ast.Stmt {
	var found ast.Stmt
	bestSpan := int(^uint(0) >> 1)
	ast.Inspect(body, func(node ast.Node) bool {
		var statement ast.Stmt
		switch value := node.(type) {
		case *ast.IfStmt:
			statement = value
		case *ast.ReturnStmt:
			statement = value
		default:
			return true
		}
		if statement.Pos() > target.Pos() || statement.End() < target.End() {
			return true
		}
		span := int(statement.End() - statement.Pos())
		if span < bestSpan {
			found = statement
			bestSpan = span
		}
		return true
	})
	return found
}

func destructiveCallBindings(body *ast.BlockStmt, call *ast.CallExpr) (string, string) {
	var result, errorName string
	bestSpan := int(^uint(0) >> 1)
	ast.Inspect(body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) == 0 {
			return true
		}
		if assignment.Pos() > call.Pos() || assignment.End() < call.End() {
			return true
		}
		span := int(assignment.End() - assignment.Pos())
		if span >= bestSpan {
			return true
		}
		identifier, ok := assignment.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if len(assignment.Lhs) > 1 {
			result = identifier.Name
		}
		if identifier, ok := assignment.Lhs[len(assignment.Lhs)-1].(*ast.Ident); ok {
			errorName = identifier.Name
		}
		bestSpan = span
		return true
	})
	return result, errorName
}

func hasFollowingActivity(body *ast.BlockStmt, destructive destructiveCallSite, calls []destructiveCallSite) bool {
	block, index := containingBlockStatement(body, destructive.Call)
	if block == nil {
		return false
	}
	return activityGuaranteed(block.List[index+1:], destructive, calls)
}

func activityGuaranteed(statements []ast.Stmt, destructive destructiveCallSite, calls []destructiveCallSite) bool {
	if len(statements) == 0 {
		return false
	}
	statement := statements[0]
	remaining := statements[1:]
	if statementLogsActivity(statement, destructive.RequiredAction) {
		return true
	}
	if statementCallsDestructiveAction(statement, destructive.RequiredAction, calls) {
		return false
	}
	switch value := statement.(type) {
	case *ast.ReturnStmt, *ast.BranchStmt:
		return false
	case *ast.ExprStmt:
		if isPanicCall(value.X) {
			return false
		}
	case *ast.IfStmt:
		if isMutationErrorGuard(value, destructive.Error) {
			return activityGuaranteed(remaining, destructive, calls)
		}
		if destructive.Result != "" && requiresNonZeroResult(value.Cond, destructive.Result) {
			return activityGuaranteed(value.Body.List, destructive, calls)
		}
		thenPath := appendStatements(value.Body.List, remaining)
		elsePath := appendStatements(elseStatements(value.Else), remaining)
		return activityGuaranteed(thenPath, destructive, calls) &&
			activityGuaranteed(elsePath, destructive, calls)
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return false
	}
	return activityGuaranteed(remaining, destructive, calls)
}

func containingBlockStatement(body *ast.BlockStmt, target ast.Node) (*ast.BlockStmt, int) {
	var found *ast.BlockStmt
	foundIndex := -1
	bestSpan := int(^uint(0) >> 1)
	ast.Inspect(body, func(node ast.Node) bool {
		block, ok := node.(*ast.BlockStmt)
		if !ok || block.Pos() > target.Pos() || block.End() < target.End() {
			return true
		}
		for i, statement := range block.List {
			if statement.Pos() > target.Pos() || statement.End() < target.End() {
				continue
			}
			span := int(block.End() - block.Pos())
			if span < bestSpan {
				found = block
				foundIndex = i
				bestSpan = span
			}
			break
		}
		return true
	})
	return found, foundIndex
}

func statementLogsActivity(statement ast.Stmt, action string) bool {
	return statementChecksAuditCall(statement, "logActivity", action)
}

func statementChecksAuditCall(statement ast.Stmt, functionName, action string) bool {
	switch value := statement.(type) {
	case *ast.ReturnStmt:
		for _, expression := range value.Results {
			call, ok := unparenExpression(expression).(*ast.CallExpr)
			if ok && auditCallMatches(call, functionName, action) {
				return true
			}
		}
	case *ast.IfStmt:
		errorName := auditCallErrorBinding(value.Init, functionName, action)
		if errorName == "" {
			return false
		}
		return isAuditErrorGuard(value, errorName)
	}
	return false
}

func auditCallErrorBinding(statement ast.Stmt, functionName, action string) string {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) == 0 || len(assignment.Rhs) != 1 {
		return ""
	}
	call, ok := unparenExpression(assignment.Rhs[0]).(*ast.CallExpr)
	if !ok || !auditCallMatches(call, functionName, action) {
		return ""
	}
	identifier, ok := assignment.Lhs[len(assignment.Lhs)-1].(*ast.Ident)
	if !ok || identifier.Name == "_" {
		return ""
	}
	return identifier.Name
}

func auditCallMatches(call *ast.CallExpr, functionName, action string) bool {
	if calledFunctionName(call) != functionName {
		return false
	}
	return action == "" || loggedActivityAction(call) == action
}

func calledFunctionName(call *ast.CallExpr) string {
	switch function := unparenExpression(call.Fun).(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	default:
		return ""
	}
}

func isAuditErrorGuard(branch *ast.IfStmt, errorName string) bool {
	if errorName == "" || branch.Else != nil || !blockPropagatesError(branch.Body, errorName) {
		return false
	}
	comparison, ok := branch.Cond.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ {
		return false
	}
	return (isNamedIdentifier(comparison.X, errorName) && isNilIdentifier(comparison.Y)) ||
		(isNamedIdentifier(comparison.Y, errorName) && isNilIdentifier(comparison.X))
}

func blockPropagatesError(block *ast.BlockStmt, errorName string) bool {
	if len(block.List) == 0 || !blockReturnsOnlyAuditError(block, errorName) {
		return false
	}
	switch final := block.List[len(block.List)-1].(type) {
	case *ast.ReturnStmt:
		return returnPropagatesAuditError(final, errorName)
	case *ast.ExprStmt:
		call, ok := final.X.(*ast.CallExpr)
		return ok && calledFunctionName(call) == "panic" &&
			len(call.Args) > 0 && expressionReferencesIdentifier(call.Args[0], errorName)
	case *ast.IfStmt:
		elseBlock, ok := final.Else.(*ast.BlockStmt)
		return ok && blockPropagatesError(final.Body, errorName) &&
			blockPropagatesError(elseBlock, errorName)
	default:
		return false
	}
}

func blockReturnsOnlyAuditError(block *ast.BlockStmt, errorName string) bool {
	valid := true
	ast.Inspect(block, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if !returnPropagatesAuditError(value, errorName) {
				valid = false
			}
			return false
		default:
			return valid
		}
	})
	return valid
}

func returnPropagatesAuditError(statement *ast.ReturnStmt, errorName string) bool {
	if len(statement.Results) == 0 {
		return false
	}
	errorResult := statement.Results[len(statement.Results)-1]
	return expressionReferencesIdentifier(errorResult, errorName)
}

func expressionReferencesIdentifier(expression ast.Expr, name string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func isMutationErrorGuard(branch *ast.IfStmt, errorName string) bool {
	if errorName == "" || branch.Else != nil || !blockTerminates(branch.Body) {
		return false
	}
	comparison, ok := branch.Cond.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ {
		return false
	}
	return (isNamedIdentifier(comparison.X, errorName) && isNilIdentifier(comparison.Y)) ||
		(isNamedIdentifier(comparison.Y, errorName) && isNilIdentifier(comparison.X))
}

func blockTerminates(block *ast.BlockStmt) bool {
	if len(block.List) == 0 {
		return false
	}
	switch final := block.List[len(block.List)-1].(type) {
	case *ast.ReturnStmt, *ast.BranchStmt:
		return true
	case *ast.ExprStmt:
		return isPanicCall(final.X)
	case *ast.IfStmt:
		elseBlock, ok := final.Else.(*ast.BlockStmt)
		return ok && blockTerminates(final.Body) && blockTerminates(elseBlock)
	default:
		return false
	}
}

func isNamedIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func isNilIdentifier(expression ast.Expr) bool {
	return isNamedIdentifier(expression, "nil")
}

func isPanicCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	identifier, ok := call.Fun.(*ast.Ident)
	return ok && identifier.Name == "panic"
}

func appendStatements(prefix, suffix []ast.Stmt) []ast.Stmt {
	combined := make([]ast.Stmt, 0, len(prefix)+len(suffix))
	combined = append(combined, prefix...)
	return append(combined, suffix...)
}

func elseStatements(statement ast.Stmt) []ast.Stmt {
	switch value := statement.(type) {
	case nil:
		return nil
	case *ast.BlockStmt:
		return value.List
	case *ast.IfStmt:
		return []ast.Stmt{value}
	default:
		return []ast.Stmt{value}
	}
}

func requiresNonZeroResult(expression ast.Expr, result string) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.NEQ {
		return false
	}
	return (expressionUsesResult(comparison.X, result) && isZeroExpression(comparison.Y)) ||
		(expressionUsesResult(comparison.Y, result) && isZeroExpression(comparison.X))
}

func expressionUsesResult(expression ast.Expr, result string) bool {
	expression = unparenExpression(expression)
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name == result
	case *ast.SelectorExpr:
		return expressionUsesResult(value.X, result)
	default:
		return false
	}
}

func isZeroExpression(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && (literal.Value == `""` || literal.Value == "0")
}

func statementCallsDestructiveAction(statement ast.Stmt, action string, calls []destructiveCallSite) bool {
	for _, call := range calls {
		if destructiveGraphStoreMethods[call.Method].Action != action {
			continue
		}
		if statement.Pos() <= call.Call.Pos() && statement.End() >= call.Call.End() {
			return true
		}
	}
	return false
}

func loggedActivityAction(call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "logActivity" || len(call.Args) < 3 {
		return ""
	}
	action, ok := call.Args[2].(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := action.X.(*ast.Ident)
	if !ok || pkg.Name != "store" {
		return ""
	}
	return action.Sel.Name
}
