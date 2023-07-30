package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGronMarshal(t *testing.T) {
	type D struct {
		D []map[string]any `json:"d"`
	}

	type T struct {
		A string `json:"a"`
		B int    `json:"b"`
		C bool   `json:"c"`
		D
		E       bool `json:"-"`
		private bool
	}

	value := T{
		A: "hello",
		B: 42,
		C: true,
		D: D{[]map[string]any{
			{"e": "world & i <3 restish"},
			{"f": []any{1, 2}, "g": time.Time{}, "h": []byte("foo")},
			{"for": map[int]int{1: 2}},
		}},
		private: true,
	}

	g := Gron{}
	b, err := g.Marshal(value)
	assert.NoError(t, err)
	assert.Equal(t, `body = {};
body.a = "hello";
body.b = 42;
body.c = true;
body.d = [];
body.d[0] = {};
body.d[0].e = "world & i <3 restish";
body.d[1] = {};
body.d[1].f = [];
body.d[1].f[0] = 1;
body.d[1].f[1] = 2;
body.d[1].g = "0001-01-01T00:00:00Z";
body.d[1].h = "Zm9v";
body.d[2] = {};
body.d[2].for = {};
body.d[2].for["1"] = 2;
`, string(b))

	// Invalid types should result in an error!
	_, err = g.Marshal(T{
		D: D{[]map[string]any{
			{"foo": make(chan int)},
		}},
	})
	assert.Error(t, err)
}
