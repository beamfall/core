package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestDestructiveGraphAuditCompletenessFixtures(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantMethod string
		wantAction string
	}{
		{
			name: "direct bypass",
			source: `package graph

func (h *Handler) fixtureDeleteWithoutAudit() error {
	_, err := h.store.DeleteManagedPath()
	return err
}
`,
			wantMethod: "DeleteManagedPath",
			wantAction: "ActivityManagedFileDelete",
		},
		{
			name: "classified tag delete cannot use non-seam exemption",
			source: `package graph

func (h *Handler) fixtureDeleteTagWithoutAudit() error {
	_, err := h.store.DeleteTag()
	return err
}
`,
			wantMethod: "DeleteTag",
			wantAction: "ActivityTagDelete",
		},
		{
			name: "new primitive is unclassified",
			source: `package graph

func (h *Handler) fixtureNewDestructivePrimitive() error {
	return h.store.ApplyDestructiveChange()
}
`,
			wantMethod: "ApplyDestructiveChange",
		},
		{
			name: "store alias",
			source: `package graph

func (h *Handler) fixtureStoreAlias() error {
	st := h.store
	if err := st.TrashMedia(); err != nil {
		return err
	}
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "store helper parameter",
			source: `package graph

func fixtureStoreHelper(st *store.Store) error {
	return st.ApplyDestructiveChange()
}
`,
			wantMethod: "ApplyDestructiveChange",
		},
		{
			name: "invoked closure store parameter",
			source: `package graph

func (h *Handler) fixtureClosureStoreParameter() error {
	return func(st *store.Store) error {
		return st.ApplyDestructiveChange()
	}(h.store)
}
`,
			wantMethod: "ApplyDestructiveChange",
		},
		{
			name: "method value",
			source: `package graph

func (h *Handler) fixtureMethodValue() error {
	trash := h.store.TrashMedia
	if err := trash(); err != nil {
		return err
	}
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "parenthesized method value",
			source: `package graph

func (h *Handler) fixtureParenthesizedMethodValue() error {
	trash := (h.store.TrashMedia)
	if err := trash(); err != nil {
		return err
	}
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "method value reassignment",
			source: `package graph

func (h *Handler) fixtureMethodValueReassignment() error {
	mutate := h.store.TrashMedia
	mutate = h.store.RestoreMedia
	return mutate()
}
`,
			wantMethod: "RestoreMedia",
			wantAction: "ActivityMediaRestore",
		},
		{
			name: "conditional method value reassignment",
			source: `package graph

func (h *Handler) fixtureConditionalMethodReassignment(restore bool) error {
	mutate := h.store.TrashMedia
	if restore {
		mutate = h.store.RestoreMedia
	}
	if err := mutate(); err != nil {
		return err
	}
	return h.logActivity(nil, nil, store.ActivityMediaTrash)
}
`,
			wantMethod: "<conditionally rebound Store method>",
		},
		{
			name: "conditional store alias",
			source: `package graph

func (h *Handler) fixtureConditionalStoreAlias(disable bool) error {
	st := h.store
	if disable {
		st = nil
	}
	if st != nil {
		return st.ApplyDestructiveChange()
	}
	return nil
}
`,
			wantMethod: "ApplyDestructiveChange",
		},
		{
			name: "switch method value reassignment",
			source: `package graph

func (h *Handler) fixtureSwitchMethodReassignment(restore bool) error {
	mutate := h.store.Media
	switch restore {
	case true:
		mutate = h.store.RestoreMedia
	}
	return mutate()
}
`,
			wantMethod: "<conditionally rebound Store method>",
		},
		{
			name: "loop body precedes post reassignment",
			source: `package graph

func (h *Handler) fixtureLoopPostOrder() error {
	mutate := h.store.TrashMedia
	for ; ; mutate = h.store.RestoreMedia {
		return mutate()
	}
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "loop carried method value",
			source: `package graph

func (h *Handler) fixtureLoopCarriedMethodValue() error {
	mutate := h.store.Media
	for i := 0; i < 2; mutate = h.store.RestoreMedia {
		if err := mutate(); err != nil {
			return err
		}
	}
	return nil
}
`,
			wantMethod: "RestoreMedia",
			wantAction: "ActivityMediaRestore",
		},
		{
			name: "parallel method value assignment",
			source: `package graph

func (h *Handler) fixtureParallelMethodAssignment() error {
	trash := h.store.TrashMedia
	restore := h.store.RestoreMedia
	trash, restore = restore, trash
	return restore()
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "pre-mutation audit",
			source: `package graph

func (h *Handler) fixtureAuditBeforeTrash() error {
	if err := h.logActivity(nil, nil, store.ActivityMediaTrash); err != nil {
		return err
	}
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "conditional audit bypass",
			source: `package graph

func (h *Handler) fixtureConditionalTrashAudit(audit bool) error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	if audit {
		if err := h.logActivity(nil, nil, store.ActivityMediaTrash); err != nil {
			return err
		}
	}
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "blank assignment discards audit error",
			source: `package graph

func (h *Handler) fixtureBlankAuditError() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	_ = h.logActivity(nil, nil, store.ActivityMediaTrash)
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "bare call discards audit error",
			source: `package graph

func (h *Handler) fixtureBareAuditError() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	h.logActivity(nil, nil, store.ActivityMediaTrash)
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "conditional suppresses audit error",
			source: `package graph

func (h *Handler) fixtureSuppressedAuditError() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	if err := h.logActivity(nil, nil, store.ActivityMediaTrash); err != nil {
		recordMetric(err)
	}
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "conditional early return",
			source: `package graph

func (h *Handler) fixtureConditionalEarlyReturn(skip bool) error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	if skip {
		return nil
	}
	return h.logActivity(nil, nil, store.ActivityMediaTrash)
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "result guard conditional early return",
			source: `package graph

func (h *Handler) fixtureResultGuardEarlyReturn(skip bool) error {
	batch, err := h.store.MergeApply()
	if err != nil {
		return err
	}
	if batch.ID != "" {
		if skip {
			return nil
		}
		return h.logActivity(nil, nil, store.ActivityMediaMerge)
	}
	return nil
}
`,
			wantMethod: "MergeApply",
			wantAction: "ActivityMediaMerge",
		},
		{
			name: "result name nested in unrelated guard",
			source: `package graph

func (h *Handler) fixtureUnrelatedResultGuard() error {
	batch, err := h.store.MergeApply()
	if err != nil {
		return err
	}
	if batch.ID + "-suffix" != "" {
		return h.logActivity(nil, nil, store.ActivityMediaMerge)
	}
	return nil
}
`,
			wantMethod: "MergeApply",
			wantAction: "ActivityMediaMerge",
		},
		{
			name: "panic before audit",
			source: `package graph

func (h *Handler) fixturePanicBeforeAudit() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	panic("stop")
	h.logActivity(nil, nil, store.ActivityMediaTrash)
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "goto before audit",
			source: `package graph

func (h *Handler) fixtureGotoBeforeAudit() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	goto done
	h.logActivity(nil, nil, store.ActivityMediaTrash)
done:
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "unreachable audit",
			source: `package graph

func (h *Handler) fixtureUnreachableTrashAudit() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	return nil
	h.logActivity(nil, nil, store.ActivityMediaTrash)
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
		{
			name: "invoked destructive closure",
			source: `package graph

func (h *Handler) fixtureInvokedClosure() error {
	mutate := func() error {
		return h.store.ApplyDestructiveChange()
	}
	return mutate()
}
`,
			wantMethod: "ApplyDestructiveChange",
		},
		{
			name: "uninvoked audit closure",
			source: `package graph

func (h *Handler) fixtureAuditClosure() error {
	if err := h.store.TrashMedia(); err != nil {
		return err
	}
	audit := func() error {
		return h.logActivity(nil, nil, store.ActivityMediaTrash)
	}
	_ = audit
	return nil
}
`,
			wantMethod: "TrashMedia",
			wantAction: "ActivityMediaTrash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, observed, err := inspectDestructiveGraphAudit("fixture.go", []byte(tt.source))
			if err != nil {
				t.Fatal(err)
			}
			if observed[tt.wantMethod] != 1 {
				t.Fatalf("fixture observed %s %d times, want 1", tt.wantMethod, observed[tt.wantMethod])
			}
			if len(findings) != 1 {
				t.Fatalf("fixture produced %d findings, want 1: %+v", len(findings), findings)
			}
			finding := findings[0]
			if finding.StoreMethod != tt.wantMethod || finding.RequiredAction != tt.wantAction {
				t.Fatalf("finding = %+v, want %s requiring %s", finding, tt.wantMethod, tt.wantAction)
			}
		})
	}
}

func TestStoreOwnedDestructiveAuditFixtures(t *testing.T) {
	tests := []struct {
		name    string
		results string
		body    string
		want    bool
	}{
		{
			name: "durable append present",
			body: `if err := appendActivitySQL(ctx, tx, activity); err != nil {
	return err
}
return nil`,
			want: true,
		},
		{
			name: "durable append missing",
			body: `if _, err := tx.ExecContext(ctx, "DELETE FROM media"); err != nil {
	return err
}
return nil`,
			want: false,
		},
		{
			name: "blank assignment discards append error",
			body: `_ = appendActivitySQL(ctx, tx, activity)
return nil`,
			want: false,
		},
		{
			name: "bare call discards append error",
			body: `appendActivitySQL(ctx, tx, activity)
return nil`,
			want: false,
		},
		{
			name: "conditional suppresses append error",
			body: `if err := appendActivitySQL(ctx, tx, activity); err != nil {
	recordMetric(err)
}
return nil`,
			want: false,
		},
		{
			name: "conditional success return before append error propagation",
			body: `if err := appendActivitySQL(ctx, tx, activity); err != nil {
	if activity == nil {
		return nil
	}
	return err
}
return nil`,
			want: false,
		},
		{
			name:    "conditional return puts append error outside error position",
			results: "(any, error)",
			body: `if err := appendActivitySQL(ctx, tx, activity); err != nil {
	if activity == nil {
		return err, nil
	}
	return nil, err
}
return nil, nil`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := tt.results
			if results == "" {
				results = "error"
			}
			source := `package store
func fixture(ctx any, tx any, activity any) ` + results + ` {
` + tt.body + `
}`
			file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			function, ok := file.Decls[0].(*ast.FuncDecl)
			if !ok {
				t.Fatal("fixture declaration is not a function")
			}
			if got := functionChecksNamedAudit(function, "appendActivitySQL"); got != tt.want {
				t.Fatalf("functionChecksNamedAudit = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDestructiveStoreMethodName(t *testing.T) {
	for _, method := range []string{"DeleteTag", "PurgePluginData", "TrashCollection"} {
		if !isDestructiveStoreMethodName(method) {
			t.Errorf("%s must be classified as destructive", method)
		}
	}
	for _, method := range []string{"TrashedMediaList", "TrashedMarkersForMedia"} {
		if isDestructiveStoreMethodName(method) {
			t.Errorf("%s is a read of already-trashed rows, not a destructive verb", method)
		}
	}
}
