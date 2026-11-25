package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pwnfo/gomap/internal/version"
)

// Config holds parsed command-line options and positional arguments.
type Config struct {
	Prog           string
	ShowHelp       bool
	ShowVersion    bool
	Stats          bool
	Quiet          bool
	NonInteractive bool
	ForceOverwrite bool

	ExprFile string
	Start    string
	HasStart bool
	End      string
	HasEnd   bool

	Delimiter   string
	RawDelim    bool
	WriteBuffer string
	HasBuffer   bool
	Workers     int
	Flush       string

	Output        string
	Compress      string
	CompressLevel int
	HasCompLevel  bool

	Pattern    string
	HasPattern bool
	Files      []string
}

// DefaultConfig returns a Config initialized with default option values.
func DefaultConfig(prog string) Config {
	if prog == "" {
		prog = "gomap"
	}
	return Config{
		Prog:      prog,
		Delimiter: "\n",
		Workers:   1,
		Flush:     "512KB",
		Files:     []string{},
	}
}

// WriteUsage writes the short usage synopsis line.
func WriteUsage(w io.Writer, prog string) {
	if prog == "" {
		prog = "gomap"
	}
	_, _ = fmt.Fprintf(w, "usage: %s [options] <pattern> [<files...>]\n", prog)
}

// WriteHelp writes the full categorized CLI help guide using Go single-dash flag conventions.
func WriteHelp(w io.Writer, prog string) {
	if prog == "" {
		prog = "gomap"
	}
	help := fmt.Sprintf(`usage: %[1]s [options] <pattern> [<files...>]

%[2]s

General Options:
  -h, -help                   show this help message and exit
  -v, -version                show version information and exit
  -st, -stats                 show pattern statistics and exit
  -q, -quiet                  suppress non-essential output
  -n, -non-interactive        skip the confirmation prompt before execution

Generation Options:
  -d, -delimiter <string>      string inserted between generated entries (default: "\n")
  -b, -write-buffer <size>     output I/O buffer size (default: 1MB)
  -w, -workers <0-256>         number of worker goroutines, 0 = auto CPU cores (default: 1)
  -ft, -flush-threshold <size> flush chunk buffer after reaching byte threshold (default: 512KB)

Input Options:
  -f, -file <path>             load patterns from .gmap file ("-" for stdin)
  -s, -start <word>            start writing output from <word>
  -e, -end <word>              stop writing output at <word>

Output Options:
  -o, -output <path>           write output to a file
  -fo, -force                  overwrite existing compressed output file
  -z, -compress <format>       compress output (supported: gzip, zstd, bzip2, lzma)
  -zl, -compresslevel <level>  compression level for the selected format

Examples:
  %[1]s '/A{4}'
  %[1]s '/l{4}#[0-8:2]' -w 0 -o words.txt
  %[1]s -f examples/dates_dmy.gmap -o dates.txt
  %[1]s '/H{6}' -z zstd -ft 1MB -zl 3 -o hashes.txt.zst

Developed by %[3]s
Repository:
  %[4]s
`, prog, version.Description, version.Author, version.RepoURL)

	_, _ = io.WriteString(w, help)
}

// ParseArgs parses Go-style single-dash flags (as well as --flag compatibility, matching Go's stdlib flag package)
// while allowing flags and positional arguments in any order.
func ParseArgs(prog string, args []string) (Config, error) {
	cfg := DefaultConfig(prog)
	var positionals []string
	endOfFlags := false

	nextValue := func(idx *int, rawFlag string, inlineVal *string) (string, error) {
		if inlineVal != nil {
			return *inlineVal, nil
		}
		if *idx+1 >= len(args) {
			return "", fmt.Errorf("flag needs an argument: %s", rawFlag)
		}
		*idx++
		return args[*idx], nil
	}

	applyBoolValue := func(rawFlag string, target *bool, inlineVal *string) error {
		if inlineVal == nil {
			*target = true
			return nil
		}
		b, err := strconv.ParseBool(*inlineVal)
		if err != nil {
			return fmt.Errorf("invalid boolean value %q for %s", *inlineVal, rawFlag)
		}
		*target = b
		return nil
	}

	applyValueFlag := func(canonical, rawFlag, val string) error {
		switch canonical {
		case "file":
			cfg.ExprFile = val
		case "start":
			cfg.Start = val
			cfg.HasStart = true
		case "end":
			cfg.End = val
			cfg.HasEnd = true
		case "delimiter":
			cfg.Delimiter = val
			cfg.RawDelim = true
		case "write-buffer":
			cfg.WriteBuffer = val
			cfg.HasBuffer = true
		case "workers":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid integer value %q for %s", val, rawFlag)
			}
			cfg.Workers = n
		case "flush-threshold":
			cfg.Flush = val
		case "output":
			cfg.Output = val
		case "compress":
			cfg.Compress = val
		case "compresslevel":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid integer value %q for %s", val, rawFlag)
			}
			cfg.CompressLevel = n
			cfg.HasCompLevel = true
		}
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if endOfFlags || arg == "-" || !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}

		if arg == "--" {
			endOfFlags = true
			continue
		}

		// Follow Go's standard library flag convention: strip one or two leading '-' characters.
		numMinuses := 1
		if strings.HasPrefix(arg, "--") {
			numMinuses = 2
		}
		name := arg[numMinuses:]
		if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "=") {
			return cfg, fmt.Errorf("bad flag syntax: %s", arg)
		}

		var inlineVal *string
		if eq := strings.IndexByte(name, '='); eq != -1 {
			v := name[eq+1:]
			inlineVal = &v
			name = name[:eq]
		}

		switch name {
		case "h", "help":
			if err := applyBoolValue(arg, &cfg.ShowHelp, inlineVal); err != nil {
				return cfg, err
			}
		case "v", "version":
			if err := applyBoolValue(arg, &cfg.ShowVersion, inlineVal); err != nil {
				return cfg, err
			}
		case "st", "stats":
			if err := applyBoolValue(arg, &cfg.Stats, inlineVal); err != nil {
				return cfg, err
			}
		case "q", "quiet":
			if err := applyBoolValue(arg, &cfg.Quiet, inlineVal); err != nil {
				return cfg, err
			}
		case "n", "non-interactive":
			if err := applyBoolValue(arg, &cfg.NonInteractive, inlineVal); err != nil {
				return cfg, err
			}
		case "fo", "force":
			if err := applyBoolValue(arg, &cfg.ForceOverwrite, inlineVal); err != nil {
				return cfg, err
			}
		case "f", "file":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("file", arg, val)
		case "s", "start":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("start", arg, val)
		case "e", "end":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("end", arg, val)
		case "d", "delimiter":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("delimiter", arg, val)
		case "b", "write-buffer":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("write-buffer", arg, val)
		case "w", "workers":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			if err := applyValueFlag("workers", arg, val); err != nil {
				return cfg, err
			}
		case "ft", "flush-threshold":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("flush-threshold", arg, val)
		case "o", "output":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("output", arg, val)
		case "z", "compress":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			_ = applyValueFlag("compress", arg, val)
		case "zl", "compresslevel":
			val, err := nextValue(&i, arg, inlineVal)
			if err != nil {
				return cfg, err
			}
			if err := applyValueFlag("compresslevel", arg, val); err != nil {
				return cfg, err
			}
		default:
			return cfg, fmt.Errorf("flag provided but not defined: -%s", name)
		}
	}

	if len(positionals) > 0 {
		cfg.Pattern = positionals[0]
		cfg.HasPattern = true
		if len(positionals) > 1 {
			cfg.Files = positionals[1:]
		}
	}

	return cfg, nil
}
