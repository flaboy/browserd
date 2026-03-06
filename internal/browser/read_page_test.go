package browser

import "testing"

func TestBuildPageSnapshot_GroupsActionableElements(t *testing.T) {
	rows := []snapshotRow{
		{Selector: "a:nth-of-type(1)", Group: "links", TagName: "a", Text: "News", Href: "https://example.com/news"},
		{Selector: "button:nth-of-type(1)", Group: "buttons", TagName: "button", Text: "Submit"},
	}

	page, refs := buildPageSnapshot(rows, "https://example.com", "Example")

	if page.URL != "https://example.com" {
		t.Fatalf("unexpected page url: %s", page.URL)
	}
	if _, ok := page.Groups["links"]; !ok {
		t.Fatalf("expected links group")
	}
	if _, ok := page.Groups["buttons"]; !ok {
		t.Fatalf("expected buttons group")
	}
	if refs["e1"].Ref != "e1" {
		t.Fatalf("expected first actionable ref to be e1, got %+v", refs["e1"])
	}
	if refs["e2"].Ref != "e2" {
		t.Fatalf("expected second actionable ref to be e2, got %+v", refs["e2"])
	}
}

func TestBuildPageSnapshot_AssignsTextRefsToTextsGroup(t *testing.T) {
	rows := []snapshotRow{
		{Selector: "article:nth-of-type(1)", Group: "texts", TagName: "article", Text: "Alpha paragraph", TextLength: 15},
	}

	page, refs := buildPageSnapshot(rows, "https://example.com", "Example")

	texts, ok := page.Groups["texts"]
	if !ok {
		t.Fatalf("expected texts group")
	}
	if len(texts.Rows) != 1 {
		t.Fatalf("expected one text row, got %d", len(texts.Rows))
	}
	if refs["t1"].Ref != "t1" {
		t.Fatalf("expected first text ref to be t1, got %+v", refs["t1"])
	}
	if refs["t1"].Kind != "text" {
		t.Fatalf("expected text ref kind, got %+v", refs["t1"])
	}
}

func TestBuildPageSnapshot_DropsEmptyGroups(t *testing.T) {
	page, _ := buildPageSnapshot(nil, "https://example.com", "Example")

	if len(page.Groups) != 0 {
		t.Fatalf("expected empty groups map, got %+v", page.Groups)
	}
}
