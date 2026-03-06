package runtime

import "testing"

func TestState_ReplaceSnapshotRefs_InvalidatesOldRefs(t *testing.T) {
	s := NewState()
	s.ReplaceSnapshot("rt_1", SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]RefState{
			"e1": {Ref: "e1", SnapshotID: "snap_1"},
		},
	})
	s.ReplaceSnapshot("rt_1", SnapshotState{
		SnapshotID: "snap_2",
		Refs: map[string]RefState{
			"e2": {Ref: "e2", SnapshotID: "snap_2"},
		},
	})

	_, err := s.GetRef("rt_1", "e1")
	if err == nil {
		t.Fatalf("expected stale ref error")
	}
	if err != ErrStaleRef {
		t.Fatalf("expected ErrStaleRef, got %v", err)
	}
}

func TestState_ClearSnapshotOnNavigate(t *testing.T) {
	s := NewState()
	s.ReplaceSnapshot("rt_1", SnapshotState{
		SnapshotID: "snap_1",
		Refs: map[string]RefState{
			"e1": {Ref: "e1", SnapshotID: "snap_1"},
		},
	})

	s.ClearSnapshot("rt_1")

	_, err := s.GetSnapshot("rt_1")
	if err == nil {
		t.Fatalf("expected snapshot not found")
	}
	if err != ErrSnapshotNotFound {
		t.Fatalf("expected ErrSnapshotNotFound, got %v", err)
	}
}

func TestState_GetRef_SupportsElementAndTextRefs(t *testing.T) {
	s := NewState()
	s.ReplaceSnapshot("rt_1", SnapshotState{
		SnapshotID: "snap_1",
		Page:       PageState{URL: "https://example.com"},
		Refs: map[string]RefState{
			"e1": {Ref: "e1", Kind: "element", Selector: "button:nth-of-type(1)", SnapshotID: "snap_1"},
			"t1": {Ref: "t1", Kind: "text", Selector: "article:nth-of-type(1)", SnapshotID: "snap_1"},
		},
	})

	if _, err := s.GetRef("rt_1", "e1"); err != nil {
		t.Fatalf("expected element ref, got %v", err)
	}
	if _, err := s.GetRef("rt_1", "t1"); err != nil {
		t.Fatalf("expected text ref, got %v", err)
	}
}

func TestState_ReplaceSnapshot_InvalidatesPreviousTextRefs(t *testing.T) {
	s := NewState()
	s.ReplaceSnapshot("rt_1", SnapshotState{
		SnapshotID: "snap_1",
		Page:       PageState{URL: "https://example.com/1"},
		Refs: map[string]RefState{
			"t1": {Ref: "t1", Kind: "text", SnapshotID: "snap_1"},
		},
	})
	s.ReplaceSnapshot("rt_1", SnapshotState{
		SnapshotID: "snap_2",
		Page:       PageState{URL: "https://example.com/2"},
		Refs: map[string]RefState{
			"e1": {Ref: "e1", Kind: "element", SnapshotID: "snap_2"},
		},
	})

	_, err := s.GetRef("rt_1", "t1")
	if err == nil {
		t.Fatalf("expected stale ref error")
	}
	if err != ErrStaleRef {
		t.Fatalf("expected ErrStaleRef, got %v", err)
	}
}
