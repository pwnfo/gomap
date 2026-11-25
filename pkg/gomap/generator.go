package gomap

import (
	"bytes"
	"context"
	"io"
	"math/big"
)

// ChunkHandler processes a batch of generated entries (already delimited).
// The chunk slice is reused once ChunkHandler returns; callers retaining it must copy.
type ChunkHandler func(chunk []byte, words uint64) error

type odometerSlot struct {
	pos   int
	chars []byte
}

// tryBuildFixedOdometer checks if an expression consists solely of fixed-repetition
// literals and single-byte choice classes, enabling the ~200M words/sec zero-alloc byte odometer.
func tryBuildFixedOdometer(nodes []*Node, delim []byte) ([]byte, []odometerSlot, bool) {
	wordLen := 0
	numDynamic := 0

	for _, node := range nodes {
		if node.MinRep != node.MaxRep {
			return nil, nil, false
		}
		rep := node.MinRep
		if rep == 0 {
			continue
		}

		switch node.Kind {
		case NodeClass:
			if len(node.ChoiceBytes) == 0 {
				return nil, nil, false
			}
			if len(node.ChoiceBytes) == 1 {
				wordLen += rep * len(node.ChoiceBytes[0])
				continue
			}
			for _, cb := range node.ChoiceBytes {
				if len(cb) != 1 {
					return nil, nil, false
				}
			}
			wordLen += rep
			numDynamic += rep

		case NodeFile:
			if _, err := node.LoadLines(); err != nil {
				return nil, nil, false
			}
			if len(node.linesBytes) == 0 {
				return nil, nil, false
			}
			if len(node.linesBytes) == 1 {
				wordLen += rep * len(node.linesBytes[0])
				continue
			}
			for _, lb := range node.linesBytes {
				if len(lb) != 1 {
					return nil, nil, false
				}
			}
			wordLen += rep
			numDynamic += rep

		default:
			return nil, nil, false
		}
	}

	stride := wordLen + len(delim)
	template := make([]byte, stride)
	slots := make([]odometerSlot, 0, numDynamic)
	cursor := 0

	for _, node := range nodes {
		rep := node.MinRep
		if rep == 0 {
			continue
		}

		var choices [][]byte
		if node.Kind == NodeFile {
			choices = node.linesBytes
		} else {
			choices = node.ChoiceBytes
		}

		if len(choices) == 1 {
			lit := choices[0]
			for r := 0; r < rep; r++ {
				copy(template[cursor:], lit)
				cursor += len(lit)
			}
			continue
		}

		chars := make([]byte, len(choices))
		for i, cb := range choices {
			chars[i] = cb[0]
		}
		for r := 0; r < rep; r++ {
			template[cursor] = chars[0]
			slots = append(slots, odometerSlot{pos: cursor, chars: chars})
			cursor++
		}
	}

	copy(template[cursor:], delim)
	return template, slots, true
}

func runFixedOdometer(
	template []byte,
	slots []odometerSlot,
	startIdx *big.Int,
	count uint64,
	flushThreshold int,
	chunkBuf []byte,
	onChunk ChunkHandler,
) error {
	if count == 0 {
		return nil
	}
	stride := len(template)
	if flushThreshold < stride {
		flushThreshold = stride
	}
	if flushThreshold < 4096 {
		flushThreshold = 4096
	}
	if cap(chunkBuf) < flushThreshold+stride {
		chunkBuf = make([]byte, 0, flushThreshold+stride)
	} else {
		chunkBuf = chunkBuf[:0]
	}

	if len(slots) == 0 {
		// Pure fixed literal (count == 1).
		chunkBuf = append(chunkBuf, template...)
		return onChunk(chunkBuf, 1)
	}

	// Decompose startIdx into mixed-radix digits.
	digits := make([]int, len(slots))
	if startIdx.IsUint64() {
		tmp := startIdx.Uint64()
		for j := len(slots) - 1; j >= 0; j-- {
			base := uint64(len(slots[j].chars))
			digits[j] = int(tmp % base)
			tmp /= base
			template[slots[j].pos] = slots[j].chars[digits[j]]
		}
	} else {
		tmp := new(big.Int).Set(startIdx)
		mod := new(big.Int)
		bigBase := new(big.Int)
		for j := len(slots) - 1; j >= 0; j-- {
			bigBase.SetInt64(int64(len(slots[j].chars)))
			tmp.DivMod(tmp, bigBase, mod)
			digits[j] = int(mod.Int64())
			template[slots[j].pos] = slots[j].chars[digits[j]]
		}
	}

	lastIdx := len(slots) - 1
	lastPos := slots[lastIdx].pos
	lastChars := slots[lastIdx].chars
	lastBase := len(lastChars)

	rem := count
	var wordsInChunk uint64

	for rem > 0 {
		d := digits[lastIdx]
		// Tight inner loop over the rightmost slot: zero carry checks per word!
		for d < lastBase && rem > 0 {
			template[lastPos] = lastChars[d]
			chunkBuf = append(chunkBuf, template...)
			d++
			rem--
			wordsInChunk++

			if len(chunkBuf) >= flushThreshold {
				if err := onChunk(chunkBuf, wordsInChunk); err != nil {
					return err
				}
				chunkBuf = chunkBuf[:0]
				wordsInChunk = 0
			}
		}

		if rem == 0 {
			break
		}

		// Carry propagation for higher-order positions.
		digits[lastIdx] = 0
		template[lastPos] = lastChars[0]
		for j := lastIdx - 1; j >= 0; j-- {
			nj := digits[j] + 1
			if nj < len(slots[j].chars) {
				digits[j] = nj
				template[slots[j].pos] = slots[j].chars[nj]
				break
			}
			digits[j] = 0
			template[slots[j].pos] = slots[j].chars[0]
		}
	}

	if wordsInChunk > 0 {
		if err := onChunk(chunkBuf, wordsInChunk); err != nil {
			return err
		}
	}
	return nil
}

// decomposeMixedRadix splits globalIdx across nodes into per-node 0-based coordinates.
func decomposeMixedRadix(nodes []*Node, startIdx *big.Int) ([]*big.Int, error) {
	m := len(nodes)
	coords := make([]*big.Int, m)
	if m == 0 {
		return coords, nil
	}

	caps := make([]*big.Int, m)
	for i := 0; i < m; i++ {
		c, err := nodes[i].Cardinality()
		if err != nil {
			return nil, err
		}
		caps[i] = c
	}

	tmp := new(big.Int).Set(startIdx)
	for i := m - 1; i >= 0; i-- {
		mod := new(big.Int)
		if caps[i].Sign() > 0 {
			tmp.DivMod(tmp, caps[i], mod)
		}
		coords[i] = mod
	}
	return coords, nil
}

// decomposePoolCoord decomposes a local coordinate inside a pool Node into
// (initialRep, digitCoords []uint64).
func decomposePoolCoord(baseLen uint64, minRep, maxRep int, coord *big.Int) (int, []uint64) {
	rem := new(big.Int).Set(coord)
	bigBase := new(big.Int).SetUint64(baseLen)

	for r := minRep; r <= maxRep; r++ {
		var cnt *big.Int
		if r == 0 {
			cnt = big.NewInt(1)
		} else {
			cnt = new(big.Int).Exp(bigBase, big.NewInt(int64(r)), nil)
		}
		if rem.Cmp(cnt) < 0 {
			if r == 0 {
				return 0, nil
			}
			digits := make([]uint64, r)
			mod := new(big.Int)
			for k := r - 1; k >= 0; k-- {
				rem.DivMod(rem, bigBase, mod)
				digits[k] = mod.Uint64()
			}
			return r, digits
		}
		rem.Sub(rem, cnt)
	}
	return maxRep + 1, nil
}

type generalGenState struct {
	delim          []byte
	flushThreshold int
	chunkBuf       []byte
	wordBuf        []byte
	bindings       [][]byte
	wordsInChunk   uint64
	remaining      uint64
	onChunk        ChunkHandler
}

func (st *generalGenState) emitWord() error {
	st.chunkBuf = append(st.chunkBuf, st.wordBuf...)
	st.chunkBuf = append(st.chunkBuf, st.delim...)
	st.wordsInChunk++
	st.remaining--

	if len(st.chunkBuf) >= st.flushThreshold {
		if err := st.onChunk(st.chunkBuf, st.wordsInChunk); err != nil {
			return err
		}
		st.chunkBuf = st.chunkBuf[:0]
		st.wordsInChunk = 0
	}
	return nil
}

func (st *generalGenState) walkNodes(
	nodes []*Node,
	idx int,
	seeking bool,
	initCoords []*big.Int,
	emit func() error,
) error {
	if st.remaining == 0 {
		return nil
	}
	if idx >= len(nodes) {
		return emit()
	}

	node := nodes[idx]
	prefixMark := len(st.wordBuf)

	switch node.Kind {
	case NodeClass, NodeFile:
		var pool [][]byte
		if node.Kind == NodeFile {
			if _, err := node.LoadLines(); err != nil {
				return err
			}
			pool = node.linesBytes
		} else {
			pool = node.ChoiceBytes
		}
		return st.walkPoolSlice(pool, node.MinRep, node.MaxRep, nodes, idx, prefixMark, seeking, initCoords, emit)

	case NodeRange:
		return st.walkRangeSpec(node.Range, node.MinRep, node.MaxRep, nodes, idx, prefixMark, seeking, initCoords, emit)

	case NodeBindDef:
		return st.walkBindDef(node, nodes, idx, prefixMark, seeking, initCoords, emit)

	case NodeBindRef:
		val := st.bindings[node.Slot]
		startR := node.MinRep
		if seeking && initCoords != nil {
			startR = node.MinRep + int(initCoords[idx].Int64())
		}
		firstBranch := seeking
		for r := startR; r <= node.MaxRep && st.remaining > 0; r++ {
			st.wordBuf = st.wordBuf[:prefixMark]
			for k := 0; k < r; k++ {
				st.wordBuf = append(st.wordBuf, val...)
			}
			if err := st.walkNodes(nodes, idx+1, firstBranch, initCoords, emit); err != nil {
				return err
			}
			firstBranch = false
		}
		st.wordBuf = st.wordBuf[:prefixMark]
		return nil
	}

	return nil
}

func (st *generalGenState) walkPoolSlice(
	pool [][]byte,
	minRep, maxRep int,
	nodes []*Node,
	idx int,
	prefixMark int,
	seeking bool,
	initCoords []*big.Int,
	emit func() error,
) error {
	baseLen := uint64(len(pool))
	startR := minRep
	var initDigits []uint64
	if seeking && initCoords != nil {
		startR, initDigits = decomposePoolCoord(baseLen, minRep, maxRep, initCoords[idx])
	}

	firstRep := seeking

	for r := startR; r <= maxRep && st.remaining > 0; r++ {
		if r == 0 {
			st.wordBuf = st.wordBuf[:prefixMark]
			if err := st.walkNodes(nodes, idx+1, firstRep, initCoords, emit); err != nil {
				return err
			}
			firstRep = false
			continue
		}

		var recDigits func(pos int, seekDigits bool) error
		recDigits = func(pos int, seekDigits bool) error {
			if st.remaining == 0 {
				return nil
			}
			if pos == r {
				return st.walkNodes(nodes, idx+1, seekDigits, initCoords, emit)
			}
			posMark := len(st.wordBuf)
			startD := uint64(0)
			if seekDigits && initDigits != nil {
				startD = initDigits[pos]
			}
			firstD := seekDigits
			for d := startD; d < baseLen && st.remaining > 0; d++ {
				st.wordBuf = append(st.wordBuf[:posMark], pool[d]...)
				if err := recDigits(pos+1, firstD); err != nil {
					return err
				}
				firstD = false
			}
			st.wordBuf = st.wordBuf[:posMark]
			return nil
		}

		st.wordBuf = st.wordBuf[:prefixMark]
		if err := recDigits(0, firstRep); err != nil {
			return err
		}
		firstRep = false
	}

	st.wordBuf = st.wordBuf[:prefixMark]
	return nil
}

func (st *generalGenState) walkRangeSpec(
	spec RangeSpec,
	minRep, maxRep int,
	nodes []*Node,
	idx int,
	prefixMark int,
	seeking bool,
	initCoords []*big.Int,
	emit func() error,
) error {
	baseLen := spec.Count
	startR := minRep
	var initDigits []uint64
	if seeking && initCoords != nil {
		startR, initDigits = decomposePoolCoord(baseLen, minRep, maxRep, initCoords[idx])
	}

	firstRep := seeking

	for r := startR; r <= maxRep && st.remaining > 0; r++ {
		if r == 0 {
			st.wordBuf = st.wordBuf[:prefixMark]
			if err := st.walkNodes(nodes, idx+1, firstRep, initCoords, emit); err != nil {
				return err
			}
			firstRep = false
			continue
		}

		var recDigits func(pos int, seekDigits bool) error
		recDigits = func(pos int, seekDigits bool) error {
			if st.remaining == 0 {
				return nil
			}
			if pos == r {
				return st.walkNodes(nodes, idx+1, seekDigits, initCoords, emit)
			}
			posMark := len(st.wordBuf)
			startD := uint64(0)
			if seekDigits && initDigits != nil {
				startD = initDigits[pos]
			}
			firstD := seekDigits
			for d := startD; d < baseLen && st.remaining > 0; d++ {
				st.wordBuf = spec.AppendItem(st.wordBuf[:posMark], d)
				if err := recDigits(pos+1, firstD); err != nil {
					return err
				}
				firstD = false
			}
			st.wordBuf = st.wordBuf[:posMark]
			return nil
		}

		st.wordBuf = st.wordBuf[:prefixMark]
		if err := recDigits(0, firstRep); err != nil {
			return err
		}
		firstRep = false
	}

	st.wordBuf = st.wordBuf[:prefixMark]
	return nil
}

func (st *generalGenState) walkBindDef(
	node *Node,
	nodes []*Node,
	idx int,
	prefixMark int,
	seeking bool,
	initCoords []*big.Int,
	emit func() error,
) error {
	innerCard := big.NewInt(1)
	for _, in := range node.Inner {
		c, err := in.Cardinality()
		if err != nil {
			return err
		}
		innerCard.Mul(innerCard, c)
	}

	startR := node.MinRep
	innerStart := big.NewInt(0)

	if seeking && initCoords != nil {
		rem := new(big.Int).Set(initCoords[idx])
		for r := node.MinRep; r <= node.MaxRep; r++ {
			var block *big.Int
			if r == 0 {
				block = big.NewInt(1)
			} else {
				block = innerCard
			}
			if rem.Cmp(block) < 0 {
				startR = r
				innerStart = rem
				break
			}
			rem.Sub(rem, block)
		}
	}

	firstRep := seeking
	var slotScratch []byte

	for r := startR; r <= node.MaxRep && st.remaining > 0; r++ {
		if r == 0 {
			st.wordBuf = st.wordBuf[:prefixMark]
			st.bindings[node.Slot] = nil
			if err := st.walkNodes(nodes, idx+1, firstRep, initCoords, emit); err != nil {
				return err
			}
			firstRep = false
			continue
		}

		var innerCoords []*big.Int
		if firstRep && innerStart.Sign() > 0 {
			var err error
			innerCoords, err = decomposeMixedRadix(node.Inner, innerStart)
			if err != nil {
				return err
			}
		}

		firstInner := firstRep
		st.wordBuf = st.wordBuf[:prefixMark]

		err := st.walkNodes(node.Inner, 0, firstRep && innerCoords != nil, innerCoords, func() error {
			// Bytes produced by Inner are st.wordBuf[prefixMark:].
			innerValLen := len(st.wordBuf) - prefixMark
			slotScratch = append(slotScratch[:0], st.wordBuf[prefixMark:]...)
			if r > 1 {
				for k := 1; k < r; k++ {
					slotScratch = append(slotScratch, slotScratch[:innerValLen]...)
				}
				st.wordBuf = append(st.wordBuf[:prefixMark], slotScratch...)
			}
			st.bindings[node.Slot] = slotScratch

			afterDefMark := len(st.wordBuf)
			err := st.walkNodes(nodes, idx+1, firstInner, initCoords, emit)
			st.wordBuf = st.wordBuf[:prefixMark+innerValLen]
			_ = afterDefMark
			firstInner = false
			return err
		})
		if err != nil {
			return err
		}
		firstRep = false
	}

	st.wordBuf = st.wordBuf[:prefixMark]
	return nil
}

func generateExprRange(
	exprNodes []*Node,
	numSlots int,
	localStart *big.Int,
	count uint64,
	delim []byte,
	flushThreshold int,
	onChunk ChunkHandler,
) error {
	if count == 0 {
		return nil
	}

	// Fast Path 1: Zero-allocation fixed-width byte odometer!
	if tmpl, slots, ok := tryBuildFixedOdometer(exprNodes, delim); ok {
		return runFixedOdometer(tmpl, slots, localStart, count, flushThreshold, nil, onChunk)
	}

	// Fast Path 2: General zero-string-alloc mixed-radix byte slice walker.
	if flushThreshold < 4096 {
		flushThreshold = 4096
	}
	var initCoords []*big.Int
	seeking := false
	if localStart.Sign() > 0 {
		var err error
		initCoords, err = decomposeMixedRadix(exprNodes, localStart)
		if err != nil {
			return err
		}
		seeking = true
	}

	st := &generalGenState{
		delim:          delim,
		flushThreshold: flushThreshold,
		chunkBuf:       make([]byte, 0, flushThreshold+256),
		wordBuf:        make([]byte, 0, 128),
		bindings:       make([][]byte, numSlots),
		remaining:      count,
		onChunk:        onChunk,
	}

	if err := st.walkNodes(exprNodes, 0, seeking, initCoords, st.emitWord); err != nil {
		return err
	}

	if st.wordsInChunk > 0 {
		if err := st.onChunk(st.chunkBuf, st.wordsInChunk); err != nil {
			return err
		}
	}
	return nil
}

// GenerateRange streams exactly `count` entries starting from global 0-based `startIndex`
// across the compiled Plan, invoking `onChunk` whenever `flushThreshold` bytes accumulate.
func (g *Engine) GenerateRange(
	ctx context.Context,
	plan *Plan,
	startIndex *big.Int,
	count uint64,
	delimiter string,
	flushThreshold int,
	onChunk ChunkHandler,
) error {
	if count == 0 {
		return nil
	}
	delimBytes := []byte(delimiter)
	remSkip := new(big.Int).Set(startIndex)
	remCount := count

	wrappedHandler := func(chunk []byte, words uint64) error {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		return onChunk(chunk, words)
	}

	for i, exprNodes := range plan.Expressions {
		if remCount == 0 {
			break
		}
		_, fullW, err := statsSingle(exprNodes, 0, nil)
		if err != nil {
			return err
		}
		if remSkip.Cmp(fullW) >= 0 {
			remSkip.Sub(remSkip, fullW)
			continue
		}

		availBig := new(big.Int).Sub(fullW, remSkip)
		take := remCount
		if availBig.IsUint64() && availBig.Uint64() < take {
			take = availBig.Uint64()
		}

		if err := generateExprRange(exprNodes, plan.NumSlots[i], remSkip, take, delimBytes, flushThreshold, wrappedHandler); err != nil {
			return err
		}

		remSkip.SetInt64(0)
		remCount -= take
	}

	return nil
}

// GenerateToWriter is a high-level helper that generates a bounded or full Plan into an io.Writer.
func (g *Engine) GenerateToWriter(
	ctx context.Context,
	plan *Plan,
	w io.Writer,
	delimiter string,
	startToken, endToken string,
	hasStart, hasEnd bool,
) error {
	st, err := g.Stats(plan, len(delimiter), startToken, endToken, hasStart, hasEnd)
	if err != nil {
		return err
	}
	if st.TotalWords.Sign() == 0 {
		return nil
	}
	return g.GenerateRange(ctx, plan, st.StartIndex, st.TotalWords.Uint64(), delimiter, 256*1024, func(chunk []byte, _ uint64) error {
		_, err := w.Write(chunk)
		return err
	})
}

// GenerateSlice collects all generated entries of a Plan as a string slice (intended for small patterns & tests).
func (g *Engine) GenerateSlice(plan *Plan, startToken, endToken string, hasStart, hasEnd bool) ([]string, error) {
	st, err := g.Stats(plan, 0, startToken, endToken, hasStart, hasEnd)
	if err != nil {
		return nil, err
	}
	n := int(st.TotalWords.Int64())
	if n == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, n)
	var buf bytes.Buffer
	// Emit 1 word per chunk by using empty delimiter and flushThreshold=0?
	// Even cleaner: use a sentinel or direct word collector!
	remSkip := new(big.Int).Set(st.StartIndex)
	remCount := uint64(n)

	for i, exprNodes := range plan.Expressions {
		if remCount == 0 {
			break
		}
		_, fullW, err := statsSingle(exprNodes, 0, nil)
		if err != nil {
			return nil, err
		}
		if remSkip.Cmp(fullW) >= 0 {
			remSkip.Sub(remSkip, fullW)
			continue
		}
		availBig := new(big.Int).Sub(fullW, remSkip)
		take := remCount
		if availBig.IsUint64() && availBig.Uint64() < take {
			take = availBig.Uint64()
		}

		// Use general walker directly for exact per-word emission (handles arbitrary embedded '\n' from /b cleanly!).
		var initCoords []*big.Int
		seeking := false
		if remSkip.Sign() > 0 {
			initCoords, err = decomposeMixedRadix(exprNodes, remSkip)
			if err != nil {
				return nil, err
			}
			seeking = true
		}
		genSt := &generalGenState{
			wordBuf:   make([]byte, 0, 64),
			bindings:  make([][]byte, plan.NumSlots[i]),
			remaining: take,
		}
		err = genSt.walkNodes(exprNodes, 0, seeking, initCoords, func() error {
			out = append(out, string(genSt.wordBuf))
			genSt.remaining--
			return nil
		})
		if err != nil {
			return nil, err
		}
		_ = buf
		remSkip.SetInt64(0)
		remCount -= take
	}

	return out, nil
}
