package gomap

import (
	"reflect"
	"testing"
)

func TestCharClasses(t *testing.T) {
	cases := map[byte]string{
		'd': "0123456789",
		'D': "123456789",
		'h': "0123456789abcdef",
		'H': "0123456789ABCDEF",
		'a': "abcdefghijklmnopqrstuvwxyz",
		'A': "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		's': " ",
		'o': "01234567",
		'p': "!@#$%^&*-_+=",
		'l': "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
		'b': "\n",
	}
	for k, want := range cases {
		if got := CharClasses[k]; got != want {
			t.Errorf("CharClasses[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestExpandShortcuts(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wc      string
		want    string
	}{
		{"digit outside", "/d", "/", "[0123456789]"},
		{"alpha lower", "/a", "/", "[abcdefghijklmnopqrstuvwxyz]"},
		{"alpha upper", "/A", "/", "[ABCDEFGHIJKLMNOPQRSTUVWXYZ]"},
		{"hex lower", "/h", "/", "[0123456789abcdef]"},
		{"multiple outside", "/d/a", "/", "[0123456789][abcdefghijklmnopqrstuvwxyz]"},
		{"literal + shortcut", "abc/d", "/", "abc[0123456789]"},
		{"unknown passthrough", "/z", "/", "/z"},
		{"plain literal", "hello", "/", "hello"},
		{"inside brackets single", "[/d]", "/", "[0123456789]"},
		{"inside brackets mixed", "[abc/d]", "/", "[abc0123456789]"},
		{"inside brackets multi", "[/d/a]", "/", "[0123456789abcdefghijklmnopqrstuvwxyz]"},
		{"inside brackets unknown", "[/z]", "/", "[/z]"},
		{"literal group untouched", "(/d)", "/", "(/d)"},
		{"escaped slash", `\/d`, "/", `\/d`},
		{"escaped bracket", `\[abc]`, "/", `\[abc]`},
		{"escaped pipe in bracket", `[\|abc]`, "/", `[\|abc]`},
		{"custom wildcard", "@d", "@", "[0123456789]"},
		{"custom wildcard in bracket", "[@d]", "@", "[0123456789]"},
		{"multi-char wildcard", "::d", "::", "[0123456789]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandShortcuts(tc.pattern, tc.wc); got != tc.want {
				t.Errorf("ExpandShortcuts(%q, %q) = %q, want %q", tc.pattern, tc.wc, got, tc.want)
			}
		})
	}
}

func TestFormatExpression(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		args      []string
		wantExpr  string
		wantFiles []string
	}{
		{"no files", "[ab]^", nil, "[ab]^", []string{}},
		{"single inline", "^[ab]", []string{"//inline_content"}, "inline_content[ab]", []string{}},
		{"two inlines", "^-^", []string{"//first", "//second"}, "first-second", []string{}},
		{"regular file", "^", []string{"words.txt"}, "^", []string{"words.txt"}},
		{"inline then file", "^[ab]^", []string{"//inline", "words.txt"}, "inline[ab]^", []string{"words.txt"}},
		{"file then inline", "^[ab]^", []string{"words.txt", "//inline"}, "^[ab]inline", []string{"words.txt"}},
		{"inline replaces only first", "^^", []string{"//first"}, "first^", []string{}},
		{"escaped caret untouched", `\^`, []string{"//nope"}, `\^`, []string{}},
		{"caret inside class untouched", "[^a]^", []string{"//inline"}, "[^a]inline", []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotExpr, gotFiles := FormatExpression(tc.expr, tc.args)
			if gotExpr != tc.wantExpr {
				t.Errorf("expr = %q, want %q", gotExpr, tc.wantExpr)
			}
			if !reflect.DeepEqual(gotFiles, tc.wantFiles) {
				t.Errorf("files = %v, want %v", gotFiles, tc.wantFiles)
			}
		})
	}
}
