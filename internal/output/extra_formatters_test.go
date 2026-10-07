package output_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/rest-sh/restish/v2/internal/output"
)

var errFailingWriter = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errFailingWriter
}

var (
	fmtPluginBuildOnce sync.Once
	fmtPluginBin       string
	fmtPluginBuildErr  error
	fallbackPluginOnce sync.Once
	fallbackPluginBin  string
	fallbackPluginErr  error
)

func buildFmtPlugin(t *testing.T) string {
	t.Helper()
	fmtPluginBuildOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), "restish-test-fmt-output-tests")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		_, thisFile, _, _ := runtime.Caller(0)
		dir := filepath.Dir(thisFile)
		cmd := exec.Command("go", "build", "-o", bin, "./testdata/fmtplugin")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			fmtPluginBuildErr = fmt.Errorf("build fmt plugin: %w\n%s", err, out)
			return
		}
		fmtPluginBin = bin
	})
	if fmtPluginBuildErr != nil {
		t.Fatal(fmtPluginBuildErr)
	}
	if fmtPluginBin == "" {
		t.Fatal("fmt plugin binary not set after build")
	}
	return fmtPluginBin
}

func buildFallbackPlugin(t *testing.T) string {
	t.Helper()
	fallbackPluginOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), "restish-test-fallback-output-tests")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		_, thisFile, _, _ := runtime.Caller(0)
		cmd := exec.Command("go", "build", "-o", bin, "./testdata/fallbackfmt")
		cmd.Dir = filepath.Dir(thisFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			fallbackPluginErr = fmt.Errorf("build fallback plugin: %w\n%s", err, out)
			return
		}
		fallbackPluginBin = bin
	})
	if fallbackPluginErr != nil {
		t.Fatal(fallbackPluginErr)
	}
	return fallbackPluginBin
}

func tableResp() *output.Response {
	return &output.Response{
		Proto:  "HTTP/1.1",
		Status: 200,
		Body: []any{
			map[string]any{"id": float64(1), "name": "Alice", "status": "active"},
			map[string]any{"id": float64(2), "name": "Bob", "status": "inactive"},
			map[string]any{"id": float64(3), "name": "Carol", "status": "active"},
		},
	}
}

// TestTableFormatter verifies that a 3-item array produces a Unicode table
// containing all values.
func TestTableFormatter(t *testing.T) {
	var buf bytes.Buffer
	f := &output.TableFormatter{}
	if err := f.Format(&buf, tableResp(), false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()

	// Border characters should be present.
	if !strings.Contains(got, "┌") || !strings.Contains(got, "┘") {
		t.Errorf("expected Unicode box drawing, got:\n%s", got)
	}
	// All names should appear.
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		if !strings.Contains(got, name) {
			t.Errorf("expected %q in table output, got:\n%s", name, got)
		}
	}
	// Column headers should be present.
	for _, col := range []string{"id", "name", "status"} {
		if !strings.Contains(got, col) {
			t.Errorf("expected column header %q, got:\n%s", col, got)
		}
	}
}

func TestTableFormatterOmitsHTTPPreamble(t *testing.T) {
	resp := tableResp()
	resp.Headers = map[string][]string{
		"Content-Type": {"application/json"},
		"Date":         {"Mon, 02 Jan 2006 15:04:05 GMT"},
		"Set-Cookie":   {"session=secret"},
	}

	var buf bytes.Buffer
	if err := (&output.TableFormatter{}).Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Alice", "Bob"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in table output:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"HTTP/1.1 200 OK", "Content-Type:", "Date:", "Set-Cookie:"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("table output should not include HTTP preamble %q:\n%s", notWant, got)
		}
	}
}

func TestTableFormatterPropagatesWriterError(t *testing.T) {
	err := (&output.TableFormatter{}).Format(failingWriter{}, tableResp(), false)
	if !errors.Is(err, errFailingWriter) {
		t.Fatalf("error = %v, want %v", err, errFailingWriter)
	}
}

// TestTableFormatterColumns verifies that --rsh-columns restricts the output.
func TestTableFormatterColumns(t *testing.T) {
	var buf bytes.Buffer
	f := &output.TableFormatter{Columns: []string{"name", "status"}}
	if err := f.Format(&buf, tableResp(), false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, "name") || !strings.Contains(got, "status") {
		t.Errorf("expected name/status columns, got:\n%s", got)
	}
	// "id" column must not appear.
	// Note: check for column header only (values like "1" are too generic).
	lines := strings.Split(got, "\n")
	headerLine := lines[1] // second line is the header row
	if strings.Contains(headerLine, " id ") {
		t.Errorf("id column should be excluded, got header:\n%s", headerLine)
	}
}

// TestGronFormatter verifies that a nested object is rendered as gron paths.
func TestGronFormatter(t *testing.T) {
	resp := &output.Response{
		Status: 200,
		Body: map[string]any{
			"user": map[string]any{
				"name": "Alice",
				"age":  float64(30),
			},
			"tags": []any{"go", "cli"},
		},
	}
	var buf bytes.Buffer
	f := &output.GronFormatter{}
	if err := f.Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()

	expected := []string{
		`json = {};`,
		`json.tags = [];`,
		`json.tags[0] = "go";`,
		`json.tags[1] = "cli";`,
		`json.user = {};`,
		`json.user.age = 30;`,
		`json.user.name = "Alice";`,
	}
	for _, want := range expected {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in gron output, got:\n%s", want, got)
		}
	}
}

func TestGronFormatter_EscapesNonIdentifierKeys(t *testing.T) {
	resp := &output.Response{
		Status: 200,
		Body: map[string]any{
			"a.b":  "dot",
			"1foo": "digit",
			"a b":  "space",
		},
	}
	var buf bytes.Buffer
	if err := (&output.GronFormatter{}).Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		`json["a.b"] = "dot";`,
		`json["1foo"] = "digit";`,
		`json["a b"] = "space";`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in gron output, got:\n%s", want, got)
		}
	}
}

func TestGronFormatterPropagatesWriterError(t *testing.T) {
	err := (&output.GronFormatter{}).Format(failingWriter{}, &output.Response{Body: map[string]any{"x": 1}}, false)
	if !errors.Is(err, errFailingWriter) {
		t.Fatalf("error = %v, want %v", err, errFailingWriter)
	}
}

// TestTableFormatterSortBy verifies that SortBy sorts rows by the named column.
func TestTableFormatterSortBy(t *testing.T) {
	resp := &output.Response{
		Status: 200,
		Body: []any{
			map[string]any{"name": "Zara"},
			map[string]any{"name": "Alice"},
			map[string]any{"name": "Mia"},
		},
	}
	var buf bytes.Buffer
	f := &output.TableFormatter{SortBy: "name"}
	if err := f.Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()
	alicePos := strings.Index(got, "Alice")
	miaPos := strings.Index(got, "Mia")
	zaraPos := strings.Index(got, "Zara")
	if alicePos == -1 || miaPos == -1 || zaraPos == -1 {
		t.Fatalf("missing names in output:\n%s", got)
	}
	if !(alicePos < miaPos && miaPos < zaraPos) {
		t.Errorf("expected Alice < Mia < Zara after SortBy, positions: %d %d %d\n%s", alicePos, miaPos, zaraPos, got)
	}
}

func TestTableFormatterSortByNumericColumn(t *testing.T) {
	resp := &output.Response{
		Status: 200,
		Body: []any{
			map[string]any{"id": float64(10), "name": "ten"},
			map[string]any{"id": float64(2), "name": "two"},
			map[string]any{"id": float64(12), "name": "twelve"},
			map[string]any{"id": float64(1), "name": "one"},
		},
	}
	var buf bytes.Buffer
	f := &output.TableFormatter{SortBy: "id"}
	if err := f.Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()
	onePos := strings.Index(got, "one")
	twoPos := strings.Index(got, "two")
	tenPos := strings.Index(got, "ten")
	twelvePos := strings.Index(got, "twelve")
	if onePos == -1 || twoPos == -1 || tenPos == -1 || twelvePos == -1 {
		t.Fatalf("missing names in output:\n%s", got)
	}
	if !(onePos < twoPos && twoPos < tenPos && tenPos < twelvePos) {
		t.Errorf("expected 1 < 2 < 10 < 12 after SortBy, positions: %d %d %d %d\n%s", onePos, twoPos, tenPos, twelvePos, got)
	}
}

// TestTableFormatterTruncation verifies that long cell values are truncated.
func TestTableFormatterTruncation(t *testing.T) {
	longValue := strings.Repeat("x", 50)
	resp := &output.Response{
		Status: 200,
		Body:   []any{map[string]any{"value": longValue}},
	}
	var buf bytes.Buffer
	if err := (&output.TableFormatter{}).Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, longValue) {
		t.Error("expected long value to be truncated, but found full value in output")
	}
	if !strings.Contains(got, "…") {
		t.Error("expected truncation indicator '…' in output")
	}
}

// TestTableFormatterMixedTypeRows verifies that rows with non-object items fall
// back to JSON output.
func TestTableFormatterMixedTypeRows(t *testing.T) {
	resp := &output.Response{
		Status: 200,
		Body:   []any{map[string]any{"id": 1}, "not an object"},
	}
	var buf bytes.Buffer
	if err := (&output.TableFormatter{}).Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	// Should fall back to JSON (array contains non-object item).
	got := buf.String()
	if strings.Contains(got, "┌") {
		t.Error("expected JSON fallback for mixed-type rows, got a table")
	}
}

// TestTableFormatterCJKWidth verifies that CJK characters are counted by rune,
// not byte (they would exceed truncation width if bytes were counted).
func TestTableFormatterCJKWidth(t *testing.T) {
	// 38 CJK runes — fits within the 40-rune limit.
	cjkValue := strings.Repeat("中", 38)
	resp := &output.Response{
		Status: 200,
		Body:   []any{map[string]any{"text": cjkValue}},
	}
	var buf bytes.Buffer
	if err := (&output.TableFormatter{}).Format(&buf, resp, false); err != nil {
		t.Fatalf("Format: %v", err)
	}
	got := buf.String()
	// Should not be truncated (38 runes < 40).
	if strings.Contains(got, "…") {
		t.Error("expected 38-rune CJK value to fit without truncation")
	}
}

// TestAutoValueStreamEmptyBase verifies that a nil base does not crash.
func TestAutoValueStreamEmptyBase(t *testing.T) {
	var buf bytes.Buffer
	f := &output.AutoFormatter{}
	stream, err := f.StartValueStream(&buf, nil, false)
	if err != nil {
		t.Fatalf("StartValueStream(nil base): %v", err)
	}
	if err := stream.WriteValue(map[string]any{"key": "value"}); err != nil {
		t.Fatalf("WriteValue: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(buf.String(), "key") {
		t.Errorf("expected output to contain 'key', got:\n%s", buf.String())
	}
}

// TestAutoValueStreamCloseWithoutWrites verifies that Close on an
// untouched stream does not crash.
func TestAutoValueStreamCloseWithoutWrites(t *testing.T) {
	var buf bytes.Buffer
	f := &output.AutoFormatter{}
	stream, err := f.StartValueStream(&buf, nil, false)
	if err != nil {
		t.Fatalf("StartValueStream: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestPluginFormatterFormatValueRoundTrip verifies that FormatValue sends the
// correct start/item/end event sequence to the formatter plugin and that the
// plugin's stdout is forwarded to the writer.
func TestPluginFormatterFormatValueRoundTrip(t *testing.T) {
	pluginPath := buildFmtPlugin(t)
	f := &output.PluginFormatter{PluginPath: pluginPath, FormatName: "testfmt"}

	var buf bytes.Buffer
	value := map[string]any{"hello": "world"}
	if err := f.FormatValue(&buf, value, false); err != nil {
		t.Fatalf("FormatValue: %v", err)
	}

	got := buf.String()
	// The test plugin outputs "<event>:<body_json>\n" for each message.
	// FormatValue sends start (empty body), item (with value), end.
	if !strings.Contains(got, "start:") {
		t.Errorf("expected start event in output, got:\n%s", got)
	}
	if !strings.Contains(got, `item:`) {
		t.Errorf("expected item event in output, got:\n%s", got)
	}
	if !strings.Contains(got, `"hello"`) {
		t.Errorf("expected body field in item output, got:\n%s", got)
	}
	if !strings.Contains(got, "end:") {
		t.Errorf("expected end event in output, got:\n%s", got)
	}
}

func TestPluginFormatterHostFallbackRoundTrip(t *testing.T) {
	f := &output.PluginFormatter{
		PluginPath:   buildFallbackPlugin(t),
		FormatName:   "testfallback",
		HostFallback: true,
	}

	var buf bytes.Buffer
	if err := f.FormatValue(&buf, map[string]any{"message": "cannot match command"}, false); err != nil {
		t.Fatalf("FormatValue: %v", err)
	}
	want := "{\n  \"message\": \"cannot match command\"\n}\n"
	if got := buf.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	buf.Reset()
	if err := f.FormatValue(&buf, "plain text", false); err != nil {
		t.Fatalf("FormatValue string: %v", err)
	}
	if got, want := buf.String(), "plain text\n"; got != want {
		t.Fatalf("string output = %q, want %q", got, want)
	}
}
