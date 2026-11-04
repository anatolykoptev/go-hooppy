package ops

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestIDList_Unmarshal pins the tolerant boundary: an agent prompted against
// the old comma-string MCP schema and one prompted against the int-array
// schema must reach the op with the SAME list. F3 (mutation: drop the string
// branch in IDList.UnmarshalJSON → the string rows go RED).
func TestIDList_Unmarshal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    []int
		wantErr string
	}{
		{"array", `[10,11]`, []int{10, 11}, ""},
		{"comma string", `"10,11"`, []int{10, 11}, ""},
		{"comma string, spaces", `" 10 , 11 "`, []int{10, 11}, ""},
		{"single id string", `"7"`, []int{7}, ""},
		{"order preserved", `"2003,2001,2002"`, []int{2003, 2001, 2002}, ""},
		{"empty array", `[]`, []int{}, ""},
		{"empty string", `""`, []int{}, ""},
		{"empty element", `"10,,11"`, nil, "empty element"},
		{"non-numeric", `"10,abc"`, nil, `"abc"`},
		{"float in array", `[1.5]`, nil, "array of integers"},
		{"object", `{"a":1}`, nil, "array of integers or a comma-separated string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got IDList
			err := json.Unmarshal([]byte(tc.in), &got)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Unmarshal(%s) err = %v, want one containing %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.in, err)
			}
			if !reflect.DeepEqual([]int(got), tc.want) {
				t.Errorf("Unmarshal(%s) = %v, want %v", tc.in, []int(got), tc.want)
			}
		})
	}
}

// TestIDList_InputStructDecodesBothForms drives the real input type: the
// three id-list fields of the publishing inputs decode identically from the
// string and the array form, and a nil (omitted) field stays nil — the
// "not given" marker presence semantics rely on.
func TestIDList_InputStructDecodesBothForms(t *testing.T) {
	var fromStr, fromArr ImportSearchPostInput
	if err := json.Unmarshal([]byte(`{"search_post_ids":"1,2","selected_pages_ids":"9","schedules_ids":"10,11"}`), &fromStr); err != nil {
		t.Fatalf("string form: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"search_post_ids":[1,2],"selected_pages_ids":[9],"schedules_ids":[10,11]}`), &fromArr); err != nil {
		t.Fatalf("array form: %v", err)
	}
	if !reflect.DeepEqual(fromStr, fromArr) {
		t.Errorf("string form decoded to %+v, array form to %+v — the boundary must normalise both to one value", fromStr, fromArr)
	}
	var none ImportSearchPostInput
	if err := json.Unmarshal([]byte(`{}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.SchedulesIDs != nil || none.SelectedPagesIDs != nil || none.SearchPostIDs != nil {
		t.Errorf("omitted id lists must stay nil (not given), got %+v", none)
	}
}
