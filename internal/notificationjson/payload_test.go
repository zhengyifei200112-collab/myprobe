package notificationjson

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBoundedJSONPreservesIdentityAndData(t *testing.T) {
	value := map[string]string{"message": "<>&"}
	original, _ := json.Marshal(value)
	same, err := MarshalBounded(value, len(original))
	if err != nil || string(same) != string(original) {
		t.Fatal("fitting payload identity changed")
	}
	compact, err := MarshalBounded(value, len(original)-1)
	if err != nil || len(compact) >= len(original) {
		t.Fatal("optional escaping not reduced")
	}
	var decoded map[string]string
	if err := json.Unmarshal(compact, &decoded); err != nil || decoded["message"] != value["message"] {
		t.Fatal("JSON data changed")
	}
	if _, err := MarshalBounded(strings.Repeat("a", 100), 99); !errors.Is(err, ErrTooLarge) {
		t.Fatal("actual data exceeded the wire limit")
	}
}
