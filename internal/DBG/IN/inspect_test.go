//go:build debug

package in

import "testing"

func TestInspectPage_EmptySegment(t *testing.T) {
	_, err := InspectPage("", 0)
	if err == nil {
		t.Error("expected error for empty segment")
	}
}

func TestParsePageHeader(t *testing.T) {
	buf := make([]byte, 4096)
	buf[0] = 1 // page type
	buf[2] = 5 // num items

	dump, err := ParsePageHeader(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dump.PageType != 1 {
		t.Errorf("expected page type 1, got %d", dump.PageType)
	}
	if dump.NumItems != 5 {
		t.Errorf("expected 5 items, got %d", dump.NumItems)
	}
}

func TestParsePageHeader_TooSmall(t *testing.T) {
	_, err := ParsePageHeader(make([]byte, 100))
	if err == nil {
		t.Error("expected error for small buffer")
	}
}
