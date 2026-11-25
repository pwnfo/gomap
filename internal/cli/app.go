package cli

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pwnfo/gomap/internal/compress"
	"github.com/pwnfo/gomap/internal/format"
	"github.com/pwnfo/gomap/internal/runner"
	"github.com/pwnfo/gomap/internal/script"
	"github.com/pwnfo/gomap/internal/ui"
	"github.com/pwnfo/gomap/internal/version"
	"github.com/pwnfo/gomap/pkg/gomap"
)

func dotLeader(label string, width int) string {
	if len(label) >= width {
		return label
	}
	return label + strings.Repeat(".", width-len(label))
}

func formatPreviewToken(s string) string {
	if s == "" {
		return `""`
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case 0:
			sb.WriteString(`\0`)
		default:
			if !unicode.IsPrint(r) {
				sb.WriteString(fmt.Sprintf(`\x%02x`, r))
			} else {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	rc := utf8.RuneCountInString(s)
	if rc <= maxRunes {
		return s
	}
	runes := []rune(s)
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func padRightRunes(s string, width int) string {
	rc := utf8.RuneCountInString(s)
	if rc >= width {
		return s
	}
	return s + strings.Repeat(" ", width-rc)
}

func padLeftRunes(s string, width int) string {
	rc := utf8.RuneCountInString(s)
	if rc >= width {
		return s
	}
	return strings.Repeat(" ", width-rc) + s
}

func formatCompactWords(n *big.Int) string {
	s := format.FormatBigIntCommas(n)
	if len(s) <= 21 {
		return s
	}
	// Scientific notation for astronomical counts (e.g. 52^20 -> 1.71e+34)
	bf := new(big.Float).SetPrec(128).SetInt(n)
	return bf.Text('e', 2)
}

func formatShortWords(n *big.Int) string {
	if n == nil || n.Sign() <= 0 {
		return "0"
	}
	if n.Cmp(big.NewInt(10000)) < 0 {
		return format.FormatBigIntCommas(n)
	}
	bf := new(big.Float).SetPrec(128).SetInt(n)
	units := []struct {
		thresh float64
		suffix string
	}{
		{1e12, "T"},
		{1e9, "B"},
		{1e6, "M"},
		{1e3, "K"},
	}
	val, _ := bf.Float64()
	if val >= 1e15 {
		return bf.Text('e', 1)
	}
	for _, u := range units {
		if val >= u.thresh {
			scaled := val / u.thresh
			if scaled >= 100 {
				return fmt.Sprintf("%.0f%s", scaled, u.suffix)
			}
			return fmt.Sprintf("%.1f%s", scaled, u.suffix)
		}
	}
	return format.FormatBigIntCommas(n)
}

type tableColSpec struct {
	header     string
	rightAlign bool
	values     []string
	width      int
}

func renderBoxTable(out io.Writer, cols []tableColSpec) {
	parts := make([]string, len(cols))
	drawBorder := func(left, mid, right string) {
		for i, c := range cols {
			parts[i] = strings.Repeat("─", c.width+2)
		}
		_, _ = fmt.Fprintf(out, "%s%s%s\n", left, strings.Join(parts, mid), right)
	}

	drawBorder("┌", "┬", "┐")
	hCells := make([]string, len(cols))
	for i, c := range cols {
		if c.rightAlign {
			hCells[i] = padLeftRunes(c.header, c.width)
		} else {
			hCells[i] = padRightRunes(c.header, c.width)
		}
	}
	_, _ = fmt.Fprintf(out, "│ %s │\n", strings.Join(hCells, " │ "))
	drawBorder("├", "┼", "┤")

	numRows := 0
	if len(cols) > 0 {
		numRows = len(cols[0].values)
	}
	rCells := make([]string, len(cols))
	for r := 0; r < numRows; r++ {
		for i, c := range cols {
			if c.rightAlign {
				rCells[i] = padLeftRunes(c.values[r], c.width)
			} else {
				rCells[i] = padRightRunes(c.values[r], c.width)
			}
		}
		_, _ = fmt.Fprintf(out, "│ %s │\n", strings.Join(rCells, " │ "))
	}
	drawBorder("└", "┴", "┘")
}

func writeCombinationTable(out io.Writer, rawExprs []string, summaries []gomap.ExprSummary, termWidth int) {
	if len(summaries) == 0 {
		return
	}
	if termWidth <= 0 {
		termWidth = 100
	}

	type rowData struct {
		idx            string
		expr           string
		combosFull     string
		combosShort    string
		avg            string
		previewFull    string
		previewCompact string
		previewTiny    string
	}

	rows := make([]rowData, len(summaries))
	for i, sum := range summaries {
		exprLabel := ""
		if i < len(rawExprs) {
			exprLabel = rawExprs[i]
		}

		var prevFull, prevCompact, prevTiny string
		if len(sum.HeadWords) == 0 {
			prevFull, prevCompact, prevTiny = "<empty>", "<empty>", "<empty>"
		} else if sum.AllShown {
			parts := make([]string, len(sum.HeadWords))
			for k, w := range sum.HeadWords {
				parts[k] = formatPreviewToken(w)
			}
			prevFull = strings.Join(parts, ", ")
			if len(parts) > 2 {
				prevCompact = parts[0] + " ... " + parts[len(parts)-1]
				prevTiny = parts[0] + ".." + parts[len(parts)-1]
			} else {
				prevCompact = prevFull
				prevTiny = strings.Join(parts, ",")
			}
		} else {
			heads := make([]string, len(sum.HeadWords))
			for k, w := range sum.HeadWords {
				heads[k] = formatPreviewToken(w)
			}
			tails := make([]string, len(sum.TailWords))
			for k, w := range sum.TailWords {
				tails[k] = formatPreviewToken(w)
			}
			prevFull = strings.Join(heads, ", ") + " ... " + strings.Join(tails, ", ")
			prevCompact = heads[0] + " ... " + tails[len(tails)-1]
			prevTiny = heads[0] + ".." + tails[len(tails)-1]
		}

		rows[i] = rowData{
			idx:            fmt.Sprintf("%d", i+1),
			expr:           formatPreviewToken(exprLabel),
			combosFull:     formatCompactWords(sum.Words),
			combosShort:    formatShortWords(sum.Words),
			avg:            fmt.Sprintf("%.2f", sum.AvgChars),
			previewFull:    prevFull,
			previewCompact: prevCompact,
			previewTiny:    prevTiny,
		}
	}

	pickPreview := func(r rowData, maxW int) string {
		if utf8.RuneCountInString(r.previewFull) <= maxW {
			return r.previewFull
		}
		if utf8.RuneCountInString(r.previewCompact) <= maxW {
			return r.previewCompact
		}
		if utf8.RuneCountInString(r.previewTiny) <= maxW {
			return r.previewTiny
		}
		return truncateRunes(r.previewTiny, maxW)
	}

	title := truncateRunes("Combination Preview", termWidth)
	_, _ = fmt.Fprintln(out, title)

	// Tier 4: Ultra-tiny terminal (< 40 cols) -> borderless compact lines that never wrap.
	if termWidth < 40 {
		for _, r := range rows {
			line := fmt.Sprintf("  %s. %s (%s): %s", r.idx, r.expr, r.combosShort, r.previewTiny)
			_, _ = fmt.Fprintln(out, truncateRunes(line, termWidth))
		}
		_, _ = fmt.Fprintln(out)
		return
	}

	// Tier 3: Narrow terminal (40..57 cols) -> 3 columns: Expr | Count | Sample (overhead = 10 chars)
	if termWidth < 58 {
		avail := termWidth - 10
		countVals := make([]string, len(rows))
		wCount := len("Count")
		for i, r := range rows {
			countVals[i] = r.combosShort
			if l := utf8.RuneCountInString(countVals[i]); l > wCount {
				wCount = l
			}
		}

		rem := avail - wCount
		maxExpr := rem * 2 / 5
		if maxExpr < 4 {
			maxExpr = 4
		}
		exprHeader := truncateRunes("Expr", maxExpr)
		wExpr := utf8.RuneCountInString(exprHeader)
		exprVals := make([]string, len(rows))
		for i, r := range rows {
			exprVals[i] = truncateRunes(r.expr, maxExpr)
			if l := utf8.RuneCountInString(exprVals[i]); l > wExpr {
				wExpr = l
			}
		}

		maxSample := avail - wCount - wExpr
		if maxSample < 4 {
			maxSample = 4
		}
		sampleHeader := truncateRunes("Sample", maxSample)
		wSample := utf8.RuneCountInString(sampleHeader)
		sampleVals := make([]string, len(rows))
		for i, r := range rows {
			sampleVals[i] = pickPreview(r, maxSample)
			if l := utf8.RuneCountInString(sampleVals[i]); l > wSample {
				wSample = l
			}
		}

		renderBoxTable(out, []tableColSpec{
			{header: exprHeader, rightAlign: false, values: exprVals, width: wExpr},
			{header: "Count", rightAlign: true, values: countVals, width: wCount},
			{header: sampleHeader, rightAlign: false, values: sampleVals, width: wSample},
		})
		_, _ = fmt.Fprintln(out)
		return
	}

	// Tier 2: Medium terminal (58..77 cols) -> 4 columns: # | Expr | Count | Sample (overhead = 13 chars)
	if termWidth < 78 {
		avail := termWidth - 13
		idxVals := make([]string, len(rows))
		wIdx := 1
		countVals := make([]string, len(rows))
		wCount := len("Count")
		for i, r := range rows {
			idxVals[i] = r.idx
			if l := utf8.RuneCountInString(r.idx); l > wIdx {
				wIdx = l
			}
			countVals[i] = r.combosShort
			if l := utf8.RuneCountInString(countVals[i]); l > wCount {
				wCount = l
			}
		}

		rem := avail - wIdx - wCount
		maxExpr := rem * 2 / 5
		if maxExpr < 6 {
			maxExpr = 6
		}
		wExpr := len("Expr")
		if wExpr > maxExpr {
			wExpr = maxExpr
		}
		exprVals := make([]string, len(rows))
		for i, r := range rows {
			exprVals[i] = truncateRunes(r.expr, maxExpr)
			if l := utf8.RuneCountInString(exprVals[i]); l > wExpr {
				wExpr = l
			}
		}

		maxSample := avail - wIdx - wCount - wExpr
		if maxSample < 6 {
			maxSample = 6
		}
		wSample := len("Sample")
		if wSample > maxSample {
			wSample = maxSample
		}
		sampleVals := make([]string, len(rows))
		for i, r := range rows {
			sampleVals[i] = pickPreview(r, maxSample)
			if l := utf8.RuneCountInString(sampleVals[i]); l > wSample {
				wSample = l
			}
		}

		renderBoxTable(out, []tableColSpec{
			{header: "#", rightAlign: true, values: idxVals, width: wIdx},
			{header: "Expr", rightAlign: false, values: exprVals, width: wExpr},
			{header: "Count", rightAlign: true, values: countVals, width: wCount},
			{header: "Sample", rightAlign: false, values: sampleVals, width: wSample},
		})
		_, _ = fmt.Fprintln(out)
		return
	}

	// Tier 1: Standard / Wide terminal (>= 78 cols) -> 5 columns: # | Expression | Combinations | Avg | Sample Space (overhead = 16 chars)
	avail := termWidth - 16
	idxVals := make([]string, len(rows))
	wIdx := 1
	avgVals := make([]string, len(rows))
	wAvg := len("Avg")
	combosVals := make([]string, len(rows))
	combosHeader := "Combinations"
	wCombos := len(combosHeader)

	for i, r := range rows {
		idxVals[i] = r.idx
		if l := utf8.RuneCountInString(r.idx); l > wIdx {
			wIdx = l
		}
		avgVals[i] = r.avg
		if l := utf8.RuneCountInString(r.avg); l > wAvg {
			wAvg = l
		}
		combosVals[i] = r.combosFull
		if l := utf8.RuneCountInString(r.combosFull); l > wCombos {
			wCombos = l
		}
	}

	// If full comma counts squeeze Expr + Sample below 26 chars, switch to compact Count.
	if avail-wIdx-wCombos-wAvg < 26 {
		combosHeader = "Count"
		wCombos = len(combosHeader)
		for i, r := range rows {
			combosVals[i] = r.combosShort
			if l := utf8.RuneCountInString(combosVals[i]); l > wCombos {
				wCombos = l
			}
		}
	}

	rem := avail - wIdx - wCombos - wAvg
	maxExpr := rem * 2 / 5
	if maxExpr > 36 {
		maxExpr = 36
	}
	exprHeader := "Expression"
	if maxExpr < len(exprHeader) {
		exprHeader = "Expr"
	}
	wExpr := len(exprHeader)
	exprVals := make([]string, len(rows))
	for i, r := range rows {
		exprVals[i] = truncateRunes(r.expr, maxExpr)
		if l := utf8.RuneCountInString(exprVals[i]); l > wExpr {
			wExpr = l
		}
	}

	maxSample := avail - wIdx - wCombos - wAvg - wExpr
	if maxSample > 68 {
		maxSample = 68
	}
	sampleHeader := "Sample Space"
	if maxSample < len(sampleHeader) {
		sampleHeader = "Sample"
	}
	wSample := len(sampleHeader)
	sampleVals := make([]string, len(rows))
	for i, r := range rows {
		sampleVals[i] = pickPreview(r, maxSample)
		if l := utf8.RuneCountInString(sampleVals[i]); l > wSample {
			wSample = l
		}
	}

	renderBoxTable(out, []tableColSpec{
		{header: "#", rightAlign: true, values: idxVals, width: wIdx},
		{header: exprHeader, rightAlign: false, values: exprVals, width: wExpr},
		{header: combosHeader, rightAlign: true, values: combosVals, width: wCombos},
		{header: "Avg", rightAlign: true, values: avgVals, width: wAvg},
		{header: sampleHeader, rightAlign: false, values: sampleVals, width: wSample},
	})
	_, _ = fmt.Fprintln(out)
}

func printStatsReport(
	out io.Writer,
	engine *gomap.Engine,
	plan *gomap.Plan,
	rawExprs []string,
	tokensList [][]gomap.Token,
	cfg Config,
	delim string,
) int {
	delimLen := len(delim)

	filtered, err := engine.Stats(plan, delimLen, cfg.Start, cfg.End, cfg.HasStart, cfg.HasEnd)
	if err != nil {
		return -1
	}
	unfiltered, err := engine.Stats(plan, delimLen, "", "", false, false)
	if err != nil {
		return -1
	}

	summaries, err := engine.InspectExpressions(plan)
	if err != nil {
		return -1
	}
	termWidth := 100
	if ui.IsTerminal(os.Stdout.Fd()) {
		termWidth = ui.TerminalWidth(os.Stdout.Fd())
	} else if ui.IsTerminal(os.Stderr.Fd()) {
		termWidth = ui.TerminalWidth(os.Stderr.Fd())
	}
	writeCombinationTable(out, rawExprs, summaries, termWidth)

	totalTokens := 0
	for _, t := range tokensList {
		totalTokens += len(t)
	}
	totalNodes := 0
	for _, n := range plan.Expressions {
		totalNodes += len(n)
	}

	avgLen, _, err := engine.AverageCharLengths(plan)
	if err != nil {
		return -1
	}

	delimUnit := "bytes"
	if delimLen == 1 {
		delimUnit = "byte"
	}

	_, _ = fmt.Fprintln(out, "Pattern Statistics")
	statsRows := [][2]string{
		{"Expressions", fmt.Sprintf("%d", len(plan.Expressions))},
		{"Total Tokens", fmt.Sprintf("%d", totalTokens)},
		{"Total Nodes", fmt.Sprintf("%d", totalNodes)},
		{"Delimiter", fmt.Sprintf("%q (%d %s)", delim, delimLen, delimUnit)},
		{"Average Length", fmt.Sprintf("%.2f chars", avgLen)},
	}
	for _, row := range statsRows {
		_, _ = fmt.Fprintf(out, "  %s %s\n", dotLeader(row[0], 20), row[1])
	}

	if cfg.HasStart || cfg.HasEnd {
		_, _ = fmt.Fprintln(out, "\nRange Filtering")
		pctFiltered := 0.0
		if unfiltered.TotalWords.Sign() > 0 {
			fw := new(big.Float).SetPrec(128).SetInt(filtered.TotalWords)
			uw := new(big.Float).SetPrec(128).SetInt(unfiltered.TotalWords)
			ratio, _ := new(big.Float).Quo(fw, uw).Float64()
			pctFiltered = ratio * 100.0
		}
		pctIgnored := 100.0 - pctFiltered
		ignoredWords := new(big.Int).Sub(unfiltered.TotalWords, filtered.TotalWords)
		if ignoredWords.Sign() < 0 {
			ignoredWords.SetInt64(0)
		}

		startLabel := "<None>"
		if cfg.HasStart {
			startLabel = cfg.Start
		}
		endLabel := "<None>"
		if cfg.HasEnd {
			endLabel = cfg.End
		}

		rangeRows := [][2]string{
			{"Start Word", startLabel},
			{"End Word", endLabel},
			{"Filtered Words", fmt.Sprintf("%s (%.2f%%)", format.FormatBigIntCommas(filtered.TotalWords), pctFiltered)},
			{"Ignored Words", fmt.Sprintf("%s (%.2f%%)", format.FormatBigIntCommas(ignoredWords), pctIgnored)},
		}
		for _, row := range rangeRows {
			_, _ = fmt.Fprintf(out, "  %s %s\n", dotLeader(row[0], 20), row[1])
		}
	}

	totalByteUnit := "bytes"
	if filtered.TotalBytes.Cmp(big.NewInt(1)) == 0 {
		totalByteUnit = "byte"
	}

	_, _ = fmt.Fprintln(out, "\nFinal Result")
	_, _ = fmt.Fprintf(out, "  Entries to generate: %s\n", format.FormatBigIntCommas(filtered.TotalWords))
	_, _ = fmt.Fprintf(out, "  Estimated size:      %s (%s %s)\n", format.FormatSizeBig(filtered.TotalBytes, 2), filtered.TotalBytes.String(), totalByteUnit)

	return 0
}

// Execute parses CLI arguments, compiles patterns/scripts, validates flags, and runs generation.
func Execute(ctx context.Context, prog string, rawArgs []string, stdout, stderr io.Writer) int {
	cfg, err := ParseArgs(prog, rawArgs)
	if err != nil {
		WriteUsage(stderr, prog)
		_, _ = fmt.Fprintf(stderr, "\n%s\n", err.Error())
		return 1
	}

	if cfg.ShowHelp {
		WriteHelp(stdout, cfg.Prog)
		return 0
	}
	if cfg.ShowVersion {
		_, _ = fmt.Fprintln(stdout, version.Full())
		return 0
	}

	log := ui.NewLogger(cfg.Quiet)
	log.InfoOut = stderr
	log.ErrOut = stderr

	if !cfg.HasPattern && cfg.ExprFile == "" {
		WriteHelp(stderr, cfg.Prog)
		return 1
	}
	if cfg.HasPattern && cfg.ExprFile != "" {
		log.Error("specify only one of the following: pattern or expression file (-f/-file).")
		return 1
	}

	if cfg.Workers < 0 || cfg.Workers > 256 {
		log.Error("invalid number of workers (%d). choose a value between 0 (auto) and 256.", cfg.Workers)
		return 1
	}

	var compFormat compress.Format
	var compLevelPtr *int
	if cfg.Compress != "" {
		compFormat, err = compress.ParseFormat(cfg.Compress)
		if err != nil {
			log.Error("%s", err.Error())
			return 1
		}
		if cfg.Output == "" {
			log.Error("compression requires -o/-output to specify an output file.")
			return 1
		}
		if cfg.HasCompLevel {
			lvl := cfg.CompressLevel
			if _, err := compress.ValidateLevel(compFormat, &lvl); err != nil {
				log.Error("%s", err.Error())
				return 1
			}
			compLevelPtr = &lvl
		}
	} else if cfg.HasCompLevel {
		log.Error("compression level (-zl/-compresslevel) requires -z/-compress.")
		return 1
	}

	bufferSize := compress.DefaultBufferSize
	if cfg.HasBuffer {
		parsedBuf, err := format.ParseSize(cfg.WriteBuffer)
		if err != nil || parsedBuf < 1 {
			if err == nil {
				log.Error("invalid buffer size: unbuffered mode is not supported.")
			} else {
				log.Error("invalid buffer size: %s", err.Error())
			}
			return 1
		}
		bufferSize = int(parsedBuf)
	}

	flushLimit, err := format.ParseSize(cfg.Flush)
	if err != nil || flushLimit < 1 {
		if err == nil {
			log.Error("invalid flush size: must be at least 1B.")
		} else {
			log.Error("invalid flush size: %s", err.Error())
		}
		return 1
	}

	delim := cfg.Delimiter

	engine := gomap.NewEngine()
	fullPlan := &gomap.Plan{}
	var allTokens [][]gomap.Token
	var rawExprs []string

	if cfg.ExprFile != "" {
		entries, err := script.ParseFile(cfg.ExprFile)
		if err != nil {
			log.Error("%s", err.Error())
			return 1
		}
		if len(entries) == 0 {
			return 0
		}
		for _, entry := range entries {
			toks, err := engine.Tokenize(entry.Expression)
			if err != nil {
				log.Error("%s", err.Error())
				return 1
			}
			subPlan, err := engine.Parse(toks, entry.Files)
			if err != nil {
				log.Error("%s", err.Error())
				return 1
			}
			rawExprs = append(rawExprs, gomap.SplitExpressions(entry.Expression)...)
			allTokens = append(allTokens, toks...)
			fullPlan.Expressions = append(fullPlan.Expressions, subPlan.Expressions...)
			fullPlan.NumSlots = append(fullPlan.NumSlots, subPlan.NumSlots...)
		}
	} else {
		expr, procFiles := gomap.FormatExpression(cfg.Pattern, cfg.Files)
		toks, err := engine.Tokenize(expr)
		if err != nil {
			log.Error("%s", err.Error())
			return 1
		}
		subPlan, err := engine.Parse(toks, procFiles)
		if err != nil {
			log.Error("%s", err.Error())
			return 1
		}
		rawExprs = gomap.SplitExpressions(expr)
		allTokens = toks
		fullPlan = subPlan
	}

	if cfg.Stats {
		// Validate stats first so any unreachable token / missing file error logs cleanly via Logger.
		if _, err := engine.Stats(fullPlan, len(delim), cfg.Start, cfg.End, cfg.HasStart, cfg.HasEnd); err != nil {
			log.Error("%s", err.Error())
			return 1
		}
		if _, err := engine.Stats(fullPlan, len(delim), "", "", false, false); err != nil {
			log.Error("%s", err.Error())
			return 1
		}
		return printStatsReport(stdout, engine, fullPlan, rawExprs, allTokens, cfg, delim)
	}

	log.Info("%s\n", version.Full())

	statsRes, err := engine.Stats(fullPlan, len(delim), cfg.Start, cfg.End, cfg.HasStart, cfg.HasEnd)
	if err != nil {
		log.Error("%s", err.Error())
		return 1
	}

	estSize := format.FormatSizeBig(statsRes.TotalBytes, 2)
	if compFormat == compress.FormatNone {
		log.Info("Entries to generate: %s (%s).", format.FormatBigIntCommas(statsRes.TotalWords), estSize)
	} else {
		log.Info("Entries to generate: %s (%s uncompressed, %s).", format.FormatBigIntCommas(statsRes.TotalWords), estSize, compFormat)
	}

	if !cfg.Quiet && !cfg.NonInteractive {
		if !ui.Pause(ui.DefaultPausePrompt) {
			return 0
		}
	}

	runOpts := runner.Options{
		OutputPath:     cfg.Output,
		BufferSize:     bufferSize,
		FlushThreshold: int(flushLimit),
		Workers:        cfg.Workers,
		Delimiter:      delim,
		StartToken:     cfg.Start,
		EndToken:       cfg.End,
		HasStart:       cfg.HasStart,
		HasEnd:         cfg.HasEnd,
		Compression:    compFormat,
		CompressLevel:  compLevelPtr,
		ForceOverwrite: cfg.ForceOverwrite,
		Quiet:          cfg.Quiet,
	}

	return runner.Run(ctx, engine, fullPlan, statsRes, runOpts, log)
}
