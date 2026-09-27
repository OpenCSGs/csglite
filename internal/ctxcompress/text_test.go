package ctxcompress

import (
	"fmt"
	"strings"
	"testing"
)

// assertIdempotent checks that compressing the output again changes nothing.
func assertIdempotent(t *testing.T, out, tool string, opts Options) {
	t.Helper()
	if again, _ := Text(out, tool, opts); again != out {
		t.Fatalf("second pass changed the output:\nfirst:\n%s\nsecond:\n%s", out, again)
	}
}

func TestTextLeavesSmallAndFileContentAlone(t *testing.T) {
	small := "ok\nok\nok\n"
	if out, kind := Text(small, "Bash", Options{}); out != small || kind != KindSkipped {
		t.Fatalf("small result changed: %q (%s)", out, kind)
	}

	var numbered strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&numbered, "%d\tline   \n", i)
	}
	if out, kind := Text(numbered.String(), "Bash", Options{}); out != numbered.String() || kind != KindFileRead {
		t.Fatalf("numbered file content was rewritten (%s)", kind)
	}

	repeated := strings.Repeat("same line\n", 200)
	if out, kind := Text(repeated, "Read", Options{}); out != repeated || kind != KindFileRead {
		t.Fatalf("output of the Read tool was rewritten (%s)", kind)
	}

	var code strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&code, "func f%d() {\n\treturn\n}\n\n", i)
	}
	if out, kind := Text(code.String(), "Bash", Options{}); out != code.String() || kind != KindCode {
		t.Fatalf("source code printed by a shell was rewritten (%s)", kind)
	}
}

func TestTextJSONMinifiesAndRendersTables(t *testing.T) {
	var rows []string
	for i := 0; i < 20; i++ {
		rows = append(rows, fmt.Sprintf(`  {"id": %d, "name": "item %d", "ok": true, "note": "a, b"}`, i, i))
	}
	input := "[\n" + strings.Join(rows, ",\n") + "\n]"
	out, kind := Text(input, "", Options{})
	if kind != KindJSON {
		t.Fatalf("kind = %s", kind)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "[20]{id:number,name:string,ok:bool,note:string}" {
		t.Fatalf("header = %q", lines[0])
	}
	if lines[1] != `0,item 0,true,"a, b"` || len(lines) != 21 {
		t.Fatalf("rows = %q (%d lines)", lines[1], len(lines))
	}
	assertIdempotent(t, out, "", Options{})

	object := "{\n  \"b\": 1,\n  \"a\": [1, 2, 3],\n  \"text\": \"<p>\"" + strings.Repeat(" ", 600) + "\n}"
	out, _ = Text(object, "", Options{})
	if out != `{"b":1,"a":[1,2,3],"text":"<p>"}` {
		t.Fatalf("object not minified in key order: %s", out)
	}
}

func TestTextJSONSamplingKeepsErrorsAndOutliers(t *testing.T) {
	var rows []string
	for i := 0; i < 100; i++ {
		status := "ok"
		switch i {
		case 37:
			status = "error: disk full"
		case 61:
			status = "degraded"
		}
		rows = append(rows, fmt.Sprintf(`{"id":%d,"status":%q}`, i, status))
	}
	input := "[" + strings.Join(rows, ",") + "]"

	safe, _ := Text(input, "", Options{})
	if strings.Count(safe, "\n") != 100 {
		t.Fatalf("safe mode dropped rows:\n%s", safe)
	}

	opts := Options{Sample: true}
	out, _ := Text(input, "", opts)
	for _, want := range []string{"\n0,ok", "\n37,error: disk full", "\n61,degraded", "\n99,ok", "items omitted]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sampled output misses %q:\n%s", want, out)
		}
	}
	if rows := strings.Count(out, "\n"); rows > 25 {
		t.Fatalf("sampled output kept %d rows", rows)
	}
	assertIdempotent(t, out, "", opts)
}

func TestTextLogFoldsRepeatsAndNumericChurn(t *testing.T) {
	var log strings.Builder
	log.WriteString("\x1b[32mstarting\x1b[0m   \n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&log, "downloading chunk %d of 50 (%d%%)\n", i+1, i*2)
	}
	for i := 0; i < 20; i++ {
		log.WriteString("waiting for server\n")
	}
	log.WriteString("progress 10%\rprogress 50%\rprogress 100%\n")
	log.WriteString("done\n")

	// Safe mode only counts identical lines; every numbered line survives.
	safe, kind := Text(log.String(), "Bash", Options{})
	if kind != KindLog {
		t.Fatalf("kind = %s", kind)
	}
	for _, want := range []string{
		"starting\n",
		"downloading chunk 17 of 50 (32%)\n",
		"waiting for server\n[… previous line repeated 19 more times]\n",
		"\nprogress 100%\ndone",
	} {
		if !strings.Contains(safe, want) {
			t.Fatalf("safe output misses %q:\n%s", want, safe)
		}
	}
	if strings.Contains(safe, "similar lines omitted") || strings.Contains(safe, "\x1b") {
		t.Fatalf("safe mode folded numeric lines or kept escapes:\n%q", safe)
	}
	assertIdempotent(t, safe, "Bash", Options{})

	// Aggressive mode also folds lines that differ only in their numbers.
	opts := Options{Sample: true}
	out, _ := Text(log.String(), "Bash", opts)
	want := "downloading chunk 1 of 50 (0%)\ndownloading chunk 2 of 50 (2%)\n[… 46 similar lines omitted]\ndownloading chunk 49 of 50 (96%)\ndownloading chunk 50 of 50 (98%)\n"
	if !strings.Contains(out, want) {
		t.Fatalf("aggressive output misses the folded run:\n%s", out)
	}
	assertIdempotent(t, out, "Bash", opts)
}

// Lines that differ only in numbers can still each carry information: test
// failures with their values, numbered files in a listing.
func TestTextKeepsNumberedErrorsAndListings(t *testing.T) {
	var log strings.Builder
	log.WriteString("running tests in package github.com/opencsgs/csglite/internal/example\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&log, "error: test %d failed: expected %d got %d\n", i, i, i+1)
	}
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&log, "internal/server/src/file%d.go\n", i)
	}
	log.WriteString("done\n")

	safe, _ := Text(log.String(), "Bash", Options{})
	for i := 1; i <= 8; i++ {
		for _, want := range []string{
			fmt.Sprintf("error: test %d failed: expected %d got %d", i, i, i+1),
			fmt.Sprintf("internal/server/src/file%d.go", i),
		} {
			if !strings.Contains(safe, want) {
				t.Fatalf("safe mode dropped %q:\n%s", want, safe)
			}
		}
	}

	aggressive, _ := Text(log.String(), "Bash", Options{Sample: true})
	for i := 1; i <= 8; i++ {
		if want := fmt.Sprintf("error: test %d failed: expected %d got %d", i, i, i+1); !strings.Contains(aggressive, want) {
			t.Fatalf("aggressive mode dropped error line %q:\n%s", want, aggressive)
		}
	}
}

func TestTextLogSamplingKeepsErrorsWithinBudget(t *testing.T) {
	var log strings.Builder
	for i := 0; i < 1000; i++ {
		switch i {
		case 500:
			log.WriteString("FAIL: TestSomething expected 3 got 4\n")
		default:
			fmt.Fprintf(&log, "step %s completed\n", strings.Repeat("x", i%7+1))
		}
	}
	input := log.String()
	if out, _ := Text(input, "Bash", Options{}); strings.Contains(out, "lines omitted") {
		t.Fatal("safe mode sampled lines")
	}

	opts := Options{Sample: true, MaxLines: 100}
	out, _ := Text(input, "Bash", opts)
	lines := strings.Split(out, "\n")
	if len(lines) > 100 {
		t.Fatalf("kept %d lines, budget 100", len(lines))
	}
	if !strings.Contains(out, "FAIL: TestSomething") || !strings.Contains(out, "lines omitted]") {
		t.Fatalf("error line or marker missing:\n%s", out)
	}
	assertIdempotent(t, out, "Bash", opts)
}

func TestTextSearchGroupsByFile(t *testing.T) {
	var grep strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&grep, "internal/server/routes.go:%d:\tmux.HandleFunc(%d)\n", i*10, i)
	}
	grep.WriteString("internal/server/auth.go:12:func auth() {}\n")
	fmt.Fprintf(&grep, "bin/csghub-lite:%s\n", strings.Repeat("\x01garbage", 400))
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&grep, "web/src/api/client.ts:%d:  const x = %d;\n", i, i)
	}

	out, kind := Text(grep.String(), "Grep", Options{})
	if kind != KindSearch {
		t.Fatalf("kind = %s", kind)
	}
	for _, want := range []string{
		"internal/server/routes.go\n  10:\tmux.HandleFunc(1)\n  20:\tmux.HandleFunc(2)\n",
		"\ninternal/server/auth.go:12:func auth() {}\n",
		"bytes omitted]…",
		"\nweb/src/api/client.ts\n  1:  const x = 1;\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output misses %q:\n%s", want, out)
		}
	}
	assertIdempotent(t, out, "Grep", Options{})
}

func TestTextAggressiveFactorsSharedPrefixes(t *testing.T) {
	var log strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&log, "build (windows-latest)\tRun tests\tok   github.com/opencsgs/csglite/internal/pkg%c\n", 'a'+i%26)
	}
	opts := Options{Sample: true}
	out, _ := Text(log.String(), "Bash", opts)
	if !strings.HasPrefix(out, `[the next 40 lines each start with: "build (windows-latest)\tRun tests\tok   github.com/opencsgs/csglite/internal/"]`) {
		t.Fatalf("prefix not factored:\n%s", out)
	}
	assertIdempotent(t, out, "Bash", opts)
	if safe, _ := Text(log.String(), "Bash", Options{}); strings.Contains(safe, "[the next") {
		t.Fatal("safe mode factored prefixes")
	}
}

func TestTrimLineKeepsUTF8Valid(t *testing.T) {
	line := strings.Repeat("中文", 2000)
	out := trimLine(line, 1001)
	if !strings.Contains(out, "bytes omitted") || !strings.HasPrefix(out, "中文") {
		t.Fatalf("trimmed line = %q", out[:40])
	}
	for _, r := range out {
		if r == '\uFFFD' {
			t.Fatal("trimLine split a UTF-8 sequence")
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(strings.Repeat("abcdefg", 100)); got != 200 {
		t.Fatalf("ASCII estimate = %d, want 200", got)
	}
	if got := EstimateTokens(strings.Repeat("中", 120)); got != 100 {
		t.Fatalf("CJK estimate = %d, want 100", got)
	}
}
