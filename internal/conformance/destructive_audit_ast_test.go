package conformance

import (
	"go/ast"
	"go/token"
)

type graphBinding struct {
	store     bool
	method    string
	function  *ast.FuncLit
	ambiguous bool
}

type graphBindings map[string]graphBinding

func collectGraphStoreCalls(function *ast.FuncDecl) []destructiveCallSite {
	var calls []destructiveCallSite
	seen := map[graphCallKey]bool{}
	bindings := graphBindings{}
	for _, field := range function.Type.Params.List {
		if !isStoreType(field.Type) {
			continue
		}
		for _, name := range field.Names {
			bindings[name.Name] = graphBinding{store: true}
		}
	}
	walkGraphBlock(function.Body, bindings, func(call *ast.CallExpr, method string) {
		key := graphCallKey{position: call.Pos(), method: method}
		if seen[key] {
			return
		}
		seen[key] = true
		result, errorName := destructiveCallBindings(function.Body, call)
		requirement := destructiveGraphStoreMethods[method]
		calls = append(calls, destructiveCallSite{
			Call:           call,
			Method:         method,
			Result:         result,
			Error:          errorName,
			RequiredAction: requirement.Action,
			AuditOwner:     requirement.Owner,
		})
	})
	return calls
}

type graphCallKey struct {
	position token.Pos
	method   string
}

func isStoreType(expression ast.Expr) bool {
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Store"
}

func walkGraphBlock(block *ast.BlockStmt, parent graphBindings, visit func(*ast.CallExpr, string)) {
	bindings := cloneGraphBindings(parent)
	for _, statement := range block.List {
		walkGraphStatement(statement, bindings, visit)
	}
}

func walkGraphStatement(statement ast.Stmt, bindings graphBindings, visit func(*ast.CallExpr, string)) {
	switch value := statement.(type) {
	case *ast.AssignStmt:
		for _, expression := range value.Rhs {
			walkGraphExpression(expression, bindings, visit)
		}
		bindGraphAssignments(value.Lhs, value.Rhs, bindings)
	case *ast.DeclStmt:
		declaration, ok := value.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, specification := range declaration.Specs {
			bindGraphValueSpec(specification, bindings, visit)
		}
	case *ast.ExprStmt:
		walkGraphExpression(value.X, bindings, visit)
	case *ast.ReturnStmt:
		for _, expression := range value.Results {
			walkGraphExpression(expression, bindings, visit)
		}
	case *ast.IfStmt:
		scoped := cloneGraphBindings(bindings)
		if value.Init != nil {
			walkGraphStatement(value.Init, scoped, visit)
		}
		walkGraphExpression(value.Cond, scoped, visit)
		thenBindings := cloneGraphBindings(scoped)
		walkGraphStatements(value.Body.List, thenBindings, visit)
		elseBindings := cloneGraphBindings(scoped)
		if value.Else != nil {
			if block, ok := value.Else.(*ast.BlockStmt); ok {
				walkGraphStatements(block.List, elseBindings, visit)
			} else {
				walkGraphStatement(value.Else, elseBindings, visit)
			}
		}
		mergeConditionalGraphBindings(bindings, thenBindings, elseBindings)
	case *ast.ForStmt:
		beforeLoop := cloneGraphBindings(bindings)
		scoped := cloneGraphBindings(bindings)
		if value.Init != nil {
			walkGraphStatement(value.Init, scoped, visit)
		}
		if value.Cond != nil {
			walkGraphExpression(value.Cond, scoped, visit)
		}
		for iteration := 0; iteration < 3; iteration++ {
			before := cloneGraphBindings(scoped)
			walkGraphStatements(value.Body.List, scoped, visit)
			if blockTerminates(value.Body) {
				break
			}
			if value.Post != nil {
				walkGraphStatement(value.Post, scoped, visit)
			}
			if equalGraphBindings(before, scoped) {
				break
			}
		}
		mergeGraphBindingBranches(bindings, []graphBindings{beforeLoop, scoped})
	case *ast.RangeStmt:
		beforeRange := cloneGraphBindings(bindings)
		walkGraphExpression(value.X, bindings, visit)
		scoped := cloneGraphBindings(bindings)
		clearGraphBinding(value.Key, scoped)
		clearGraphBinding(value.Value, scoped)
		walkGraphStatements(value.Body.List, scoped, visit)
		mergeGraphBindingBranches(bindings, []graphBindings{beforeRange, scoped})
	case *ast.BlockStmt:
		walkGraphBlock(value, bindings, visit)
	case *ast.GoStmt:
		walkGraphExpression(value.Call, bindings, visit)
	case *ast.DeferStmt:
		walkGraphExpression(value.Call, bindings, visit)
	case *ast.LabeledStmt:
		walkGraphStatement(value.Stmt, bindings, visit)
	case *ast.SendStmt:
		walkGraphExpression(value.Chan, bindings, visit)
		walkGraphExpression(value.Value, bindings, visit)
	case *ast.SwitchStmt:
		walkGraphSwitch(value.Init, value.Tag, value.Body, bindings, visit)
	case *ast.TypeSwitchStmt:
		before := cloneGraphBindings(bindings)
		scoped := cloneGraphBindings(bindings)
		if value.Init != nil {
			walkGraphStatement(value.Init, scoped, visit)
		}
		if value.Assign != nil {
			walkGraphStatement(value.Assign, scoped, visit)
		}
		walkGraphSwitch(nil, nil, value.Body, scoped, visit)
		mergeGraphBindingBranches(bindings, []graphBindings{before, scoped})
	case *ast.SelectStmt:
		walkGraphSwitch(nil, nil, value.Body, bindings, visit)
	}
}

func walkGraphSwitch(init ast.Stmt, tag ast.Expr, body *ast.BlockStmt, bindings graphBindings, visit func(*ast.CallExpr, string)) {
	scoped := cloneGraphBindings(bindings)
	branches := []graphBindings{cloneGraphBindings(scoped)}
	if init != nil {
		walkGraphStatement(init, scoped, visit)
	}
	if tag != nil {
		walkGraphExpression(tag, scoped, visit)
	}
	for _, statement := range body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			if communication, ok := statement.(*ast.CommClause); ok {
				clauseBindings := cloneGraphBindings(scoped)
				if communication.Comm != nil {
					walkGraphStatement(communication.Comm, clauseBindings, visit)
				}
				for _, child := range communication.Body {
					walkGraphStatement(child, clauseBindings, visit)
				}
				branches = append(branches, clauseBindings)
			}
			continue
		}
		clauseBindings := cloneGraphBindings(scoped)
		for _, expression := range clause.List {
			walkGraphExpression(expression, clauseBindings, visit)
		}
		for _, child := range clause.Body {
			walkGraphStatement(child, clauseBindings, visit)
		}
		branches = append(branches, clauseBindings)
	}
	mergeGraphBindingBranches(bindings, branches)
}

func walkGraphStatements(statements []ast.Stmt, bindings graphBindings, visit func(*ast.CallExpr, string)) {
	for _, statement := range statements {
		walkGraphStatement(statement, bindings, visit)
	}
}

func walkGraphExpression(expression ast.Expr, bindings graphBindings, visit func(*ast.CallExpr, string)) {
	ast.Inspect(expression, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if method := graphStoreMethod(value, bindings); method != "" {
				visit(value, method)
			}
			walkInvokedGraphClosure(value, bindings, visit)
		}
		return true
	})
}

func walkInvokedGraphClosure(call *ast.CallExpr, bindings graphBindings, visit func(*ast.CallExpr, string)) {
	function := unparenExpression(call.Fun)
	if literal, ok := function.(*ast.FuncLit); ok {
		scoped := cloneGraphBindings(bindings)
		bindInvokedClosureArguments(literal, call.Args, scoped)
		walkGraphBlock(literal.Body, scoped, visit)
		return
	}
	identifier, ok := function.(*ast.Ident)
	if !ok {
		return
	}
	binding := bindings[identifier.Name]
	if binding.function == nil {
		return
	}
	scoped := cloneGraphBindings(bindings)
	delete(scoped, identifier.Name)
	bindInvokedClosureArguments(binding.function, call.Args, scoped)
	walkGraphBlock(binding.function.Body, scoped, visit)
}

func bindInvokedClosureArguments(function *ast.FuncLit, arguments []ast.Expr, bindings graphBindings) {
	index := 0
	for _, field := range function.Type.Params.List {
		for _, name := range field.Names {
			if index < len(arguments) {
				bindings[name.Name] = graphBindingForExpression(arguments[index], bindings)
			} else if isStoreType(field.Type) {
				bindings[name.Name] = graphBinding{store: true}
			}
			index++
		}
	}
}

func graphStoreMethod(call *ast.CallExpr, bindings graphBindings) string {
	expression := unparenExpression(call.Fun)
	if method, ok := expression.(*ast.Ident); ok {
		binding := bindings[method.Name]
		if binding.ambiguous {
			return "<conditionally rebound Store method>"
		}
		return binding.method
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || !isGraphStoreExpression(selector.X, bindings) {
		return ""
	}
	return selector.Sel.Name
}

func bindGraphAssignments(left, right []ast.Expr, bindings graphBindings) {
	snapshot := cloneGraphBindings(bindings)
	resolved := make([]graphBinding, len(left))
	for index := range left {
		if index < len(right) {
			resolved[index] = graphBindingForExpression(right[index], snapshot)
		}
	}
	for index, lhs := range left {
		bindGraphName(lhs, resolved[index], bindings)
	}
}

func bindGraphValueSpec(specification ast.Spec, bindings graphBindings, visit func(*ast.CallExpr, string)) {
	values, ok := specification.(*ast.ValueSpec)
	if !ok {
		return
	}
	for _, expression := range values.Values {
		walkGraphExpression(expression, bindings, visit)
	}
	left := make([]ast.Expr, len(values.Names))
	for index := range values.Names {
		left[index] = values.Names[index]
	}
	bindGraphAssignments(left, values.Values, bindings)
}

func graphBindingForExpression(expression ast.Expr, bindings graphBindings) graphBinding {
	expression = unparenExpression(expression)
	if isGraphStoreExpression(expression, bindings) {
		return graphBinding{store: true}
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return bindings[identifier.Name]
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if ok && isGraphStoreExpression(selector.X, bindings) {
		return graphBinding{method: selector.Sel.Name}
	}
	if function, ok := expression.(*ast.FuncLit); ok {
		return graphBinding{function: function}
	}
	return graphBinding{}
}

func bindGraphName(expression ast.Expr, binding graphBinding, bindings graphBindings) {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return
	}
	if !binding.store && binding.method == "" && binding.function == nil && !binding.ambiguous {
		delete(bindings, identifier.Name)
		return
	}
	bindings[identifier.Name] = binding
}

func clearGraphBinding(expression ast.Expr, bindings graphBindings) {
	bindGraphName(expression, graphBinding{}, bindings)
}

func isGraphStoreExpression(expr ast.Expr, bindings graphBindings) bool {
	expr = unparenExpression(expr)
	switch value := expr.(type) {
	case *ast.Ident:
		binding := bindings[value.Name]
		return binding.store || binding.ambiguous
	case *ast.SelectorExpr:
		return value.Sel.Name == "store"
	default:
		return false
	}
}

func unparenExpression(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func cloneGraphBindings(source graphBindings) graphBindings {
	cloned := make(graphBindings, len(source))
	for name, binding := range source {
		cloned[name] = binding
	}
	return cloned
}

func mergeConditionalGraphBindings(target, thenBindings, elseBindings graphBindings) {
	mergeGraphBindingBranches(target, []graphBindings{thenBindings, elseBindings})
}

func mergeGraphBindingBranches(target graphBindings, branches []graphBindings) {
	for name := range target {
		binding := branches[0][name]
		same := true
		for _, branch := range branches[1:] {
			if branch[name] != binding {
				same = false
				break
			}
		}
		if same {
			target[name] = binding
			continue
		}
		target[name] = graphBinding{ambiguous: true}
	}
}

func equalGraphBindings(left, right graphBindings) bool {
	if len(left) != len(right) {
		return false
	}
	for name, binding := range left {
		if right[name] != binding {
			return false
		}
	}
	return true
}
