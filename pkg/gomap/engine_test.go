package gomap

import (
	"bytes"
	"context"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeAndErrors(t *testing.T) {
	eng := NewEngine()

	t.Run("plain literal", func(t *testing.T) {
		toks, err := eng.Tokenize("abc")
		if err != nil {
			t.Fatal(err)
		}
		if len(toks) != 1 || len(toks[0]) != 3 {
			t.Fatalf("unexpected tokens: %+v", toks)
		}
	})

	t.Run("numeric ranges and zero padding", func(t *testing.T) {
		toks, err := eng.Tokenize("#[0-3]")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"0", "1", "2", "3"}
		if !reflect.DeepEqual(toks[0][0].Choices, want) {
			t.Fatalf("got %v, want %v", toks[0][0].Choices, want)
		}

		toksPad, err := eng.Tokenize("#[01-04]")
		if err != nil {
			t.Fatal(err)
		}
		wantPad := []string{"01", "02", "03", "04"}
		if !reflect.DeepEqual(toksPad[0][0].Choices, wantPad) {
			t.Fatalf("got %v, want %v", toksPad[0][0].Choices, wantPad)
		}

		toksDesc, err := eng.Tokenize("#[10-0:-3]")
		if err != nil {
			t.Fatal(err)
		}
		wantDesc := []string{"10", "7", "4", "1"}
		if !reflect.DeepEqual(toksDesc[0][0].Choices, wantDesc) {
			t.Fatalf("got %v, want %v", toksDesc[0][0].Choices, wantDesc)
		}
	})

	errCases := []struct {
		pattern string
		substr  string
	}{
		{`\`, "Invalid escape"},
		{`[abc`, "Unclosed character class"},
		{`[]`, "Empty character class"},
		{`(hello`, "Unclosed character class"},
		{`#[0-5:0]`, "Range step cannot be zero"},
		{`#[5-0:1]`, "Invalid range sequence"},
		{`#[0-5`, "Unclosed range"},
		{`#[abc]`, "Invalid range"},
		{`[ab]{5,2}`, "min > max"},
		{`[ab]{}`, "Invalid repetition"},
		{`<@d=/d`, "Unclosed binding"},
		{`<@123=abc>`, "Invalid binding name"},
	}

	for _, ec := range errCases {
		_, err := eng.Tokenize(ec.pattern)
		if err == nil || !strings.Contains(err.Error(), ec.substr) {
			t.Errorf("Tokenize(%q) err = %v, want substring %q", ec.pattern, err, ec.substr)
		}
	}
}

func TestGeneratePatterns(t *testing.T) {
	eng := NewEngine()

	tmpDir := t.TempDir()
	wlPath := filepath.Join(tmpDir, "words.txt")
	if err := os.WriteFile(wlPath, []byte("apple\nbanana\ncherry\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		expr  string
		files []string
		start string
		hasS  bool
		end   string
		hasE  bool
		want  []string
	}{
		{"single literal", "a", nil, "", false, "", false, []string{"a"}},
		{"coalesced literal", "admin", nil, "", false, "", false, []string{"admin"}},
		{"literal with optional last char", "Ryan?", nil, "", false, "", false, []string{"Rya", "Ryan"}},
		{"simple class", "[abc]", nil, "", false, "", false, []string{"a", "b", "c"}},
		{"two classes", "[ab][12]", nil, "", false, "", false, []string{"a1", "a2", "b1", "b2"}},
		{"quantifier range", "[ab]{1,2}", nil, "", false, "", false, []string{"a", "b", "aa", "ab", "ba", "bb"}},
		{"optional class", "[ab]?", nil, "", false, "", false, []string{"", "a", "b"}},
		{"union class", "[cat|dog|fish]", nil, "", false, "", false, []string{"cat", "dog", "fish"}},
		{"whitespace union", "[/s|admin]", nil, "", false, "", false, []string{" ", "admin"}},
		{"literal group repeat", "(ab){2}", nil, "", false, "", false, []string{"abab"}},
		{"top-level alternation", "a[12]||b[34]", nil, "", false, "", false, []string{"a1", "a2", "b3", "b4"}},
		{"file placeholder", "^-!", []string{wlPath}, "", false, "", false, []string{"apple-!", "banana-!", "cherry-!"}},
		{"basic binding", "<@d=[012]>-<@d>", nil, "", false, "", false, []string{"0-0", "1-1", "2-2"}},
		{"two bindings", "<@a=[01]><@b=[xy]><@a><@b>", nil, "", false, "", false, []string{"0x0x", "0y0y", "1x1x", "1y1y"}},
		{"binding ref repetition", "<@d=[ab]>_<@d>{2}", nil, "", false, "", false, []string{"a_aa", "b_bb"}},
		{"binding seek with start and end", "<@w=[a|bb|ccc]>-<@w>[12]", nil, "bb-bb1", true, "ccc-ccc1", true, []string{"bb-bb1", "bb-bb2", "ccc-ccc1"}},
		{"start bound", "[abc][12]", nil, "b1", true, "", false, []string{"b1", "b2", "c1", "c2"}},
		{"end bound", "[abc][12]", nil, "", false, "b1", true, []string{"a1", "a2", "b1"}},
		{"start and end bound", "[abc][12]", nil, "a2", true, "b2", true, []string{"a2", "b1", "b2"}},
		{"variable width cross-node seek", "[a|ab][b|bc]", nil, "abc", true, "", false, []string{"abc", "abb", "abbc"}},
		{"utf8 characters", "[çã][12]", nil, "", false, "", false, []string{"ç1", "ç2", "ã1", "ã2"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := eng.Compile(tc.expr, tc.files)
			if err != nil {
				t.Fatalf("Compile(%q) failed: %v", tc.expr, err)
			}

			got, err := eng.GenerateSlice(plan, tc.start, tc.end, tc.hasS, tc.hasE)
			if err != nil {
				t.Fatalf("GenerateSlice failed: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}

			// Verify exact streaming output & byte count match Stats!
			var buf bytes.Buffer
			delim := "\n"
			if err := eng.GenerateToWriter(context.Background(), plan, &buf, delim, tc.start, tc.end, tc.hasS, tc.hasE); err != nil {
				t.Fatalf("GenerateToWriter failed: %v", err)
			}
			st, err := eng.Stats(plan, len(delim), tc.start, tc.end, tc.hasS, tc.hasE)
			if err != nil {
				t.Fatalf("Stats failed: %v", err)
			}
			if st.TotalWords.Int64() != int64(len(tc.want)) {
				t.Errorf("Stats.TotalWords = %s, want %d", st.TotalWords, len(tc.want))
			}
			if st.TotalBytes.Int64() != int64(buf.Len()) {
				t.Errorf("Stats.TotalBytes = %s, actual written bytes = %d (%q)", st.TotalBytes, buf.Len(), buf.String())
			}
		})
	}
}

func TestExactStatsWithVariableLengthBindings(t *testing.T) {
	eng := NewEngine()
	patterns := []string{
		`<@w=[a|bb|ccc]>-<@w>{1,2}`,
		`<@w=[x|yyyy]>?<@w>`,
		`pre_<@a=[1|22]><@b=[k|mmm]>_<@a>_<@b>{0,2}`,
	}

	for _, pat := range patterns {
		plan, err := eng.Compile(pat, nil)
		if err != nil {
			t.Fatalf("Compile(%q): %v", pat, err)
		}

		var buf bytes.Buffer
		if err := eng.GenerateToWriter(context.Background(), plan, &buf, "\n", "", "", false, false); err != nil {
			t.Fatalf("GenerateToWriter(%q): %v", pat, err)
		}
		st, err := eng.Stats(plan, 1, "", "", false, false)
		if err != nil {
			t.Fatalf("Stats(%q): %v", pat, err)
		}
		if st.TotalBytes.Int64() != int64(buf.Len()) {
			t.Errorf("pattern %q: Stats.TotalBytes = %s, actual = %d", pat, st.TotalBytes, buf.Len())
		}

		// Also verify WordAtIndex AND CalculateSkippedStats match every single generated word!
		words, err := eng.GenerateSlice(plan, "", "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range words {
			gotW, err := eng.WordAtIndex(plan, big.NewInt(int64(i)))
			if err != nil {
				t.Fatalf("WordAtIndex(%d): %v", i, err)
			}
			if gotW != w {
				t.Errorf("WordAtIndex(%d) = %q, want %q", i, gotW, w)
			}
		}
	}
}

func TestHugeUnmaterializedRangeAndBigIntStats(t *testing.T) {
	eng := NewEngine()

	// 1) Billion-item numeric range in O(1) memory & time!
	plan, err := eng.Compile("id_#[0-1000000000]", nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := eng.Stats(plan, 1, "id_999999998", "id_1000000000", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalWords.Int64() != 3 {
		t.Fatalf("TotalWords = %s, want 3", st.TotalWords)
	}
	words, err := eng.GenerateSlice(plan, "id_999999998", "id_1000000000", true, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id_999999998", "id_999999999", "id_1000000000"}
	if !reflect.DeepEqual(words, want) {
		t.Fatalf("got %v, want %v", words, want)
	}

	// 2) Astronomical pattern exceeding uint64 (52^20 > 10^34 words) - zero overflow!
	hugePlan, err := eng.Compile("/l{20}", nil)
	if err != nil {
		t.Fatal(err)
	}
	hugeStats, err := eng.Stats(hugePlan, 1, "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	expectedWords := new(big.Int).Exp(big.NewInt(52), big.NewInt(20), nil)
	if hugeStats.TotalWords.Cmp(expectedWords) != 0 {
		t.Fatalf("TotalWords = %s, want %s", hugeStats.TotalWords, expectedWords)
	}

	// 3) Large BindDef inner space (36.5M+ words) must seek in O(depth) without materializing inner values!
	bindPlan, err := eng.Compile("<@x=/l{4}#[0-8:2]>", nil)
	if err != nil {
		t.Fatal(err)
	}
	bindStats, err := eng.Stats(bindPlan, 1, "yyyy6", "yyyy8", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if bindStats.TotalWords.Int64() != 2 {
		t.Fatalf("bindStats.TotalWords = %s, want 2", bindStats.TotalWords)
	}
	bindWords, err := eng.GenerateSlice(bindPlan, "yyyy6", "yyyy8", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bindWords, []string{"yyyy6", "yyyy8"}) {
		t.Fatalf("got %v, want [yyyy6 yyyy8]", bindWords)
	}
}
