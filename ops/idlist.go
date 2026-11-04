package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// IDList is a list of integer IDs as it travels over the wire from an agent.
// The MCP tools used to take these fields as comma-separated strings
// ("10,11") and now take int arrays ([10,11]); an agent prompted against the
// old schema keeps sending strings, so the boundary accepts BOTH and
// normalises here, once, for every input type that embeds the field. The CLI
// binds the same fields through a repeatable/comma-separated int-slice flag
// and never goes through JSON.
type IDList []int

// UnmarshalJSON decodes a JSON array of integers or a comma-separated string
// of integers ("10,11", spaces around an element allowed). An empty string is
// an empty list; an empty element ("10,,11") or a non-number is an error that
// names it. Order is preserved: batch ids bind positionally to schedule slots.
func (l *IDList) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		return nil
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("id list: %w", err)
		}
		ids, err := parseIDListString(s)
		if err != nil {
			return err
		}
		*l = ids
		return nil
	}
	var ids []int
	if err := json.Unmarshal(b, &ids); err != nil {
		return fmt.Errorf("id list: want an array of integers or a comma-separated string of integers (e.g. [10,11] or \"10,11\"): %w", err)
	}
	*l = ids
	return nil
}

func parseIDListString(s string) (IDList, error) {
	ids := IDList{}
	if strings.TrimSpace(s) == "" {
		return ids, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("id list: empty element in %q — expected comma-separated integers like \"10,11\"", s)
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("id list: %q is not an integer in %q — expected comma-separated integers like \"10,11\"", part, s)
		}
		ids = append(ids, n)
	}
	return ids, nil
}
