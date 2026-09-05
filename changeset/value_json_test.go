package changeset

import (
	"encoding/json"
	"testing"
)

func TestValueJSONRoundTrip(t *testing.T) {
	cases := []Value{
		NewValueUndefined(),
		NewValueNull(),
		NewValueInt(0),
		NewValueInt(42),
		NewValueDouble(1.5),
		NewValueText("hello"),
		NewValueBlob([]byte{0x00, 0xFF, 0x42}),
	}
	for _, in := range cases {
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal %s: %v", in, err)
		}
		var out Value
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal %s from %s: %v", in, raw, err)
		}
		if !in.Equal(out) {
			t.Errorf("round-trip %s → %s (json %s)", in, out, raw)
		}
	}
}

func TestConflictFeatureJSONRoundTrip(t *testing.T) {
	cf := ConflictFeature{
		PK:        7,
		TableName: "points",
		Items: []ConflictItem{
			{Column: 2, Base: NewValueText("a"), Theirs: NewValueText("b"), Ours: NewValueText("c")},
		},
	}
	raw, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	var back ConflictFeature
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsValid() || back.PK != 7 || back.TableName != "points" {
		t.Fatalf("got %+v", back)
	}
	if got, _ := back.Items[0].Ours.AsText(); got != "c" {
		t.Errorf("ours: %q", got)
	}
}
