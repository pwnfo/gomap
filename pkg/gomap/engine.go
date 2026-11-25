package gomap

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Engine compiles, analyzes, seeks, and executes gomap wordlist expressions.
type Engine struct {
	Wildcard string
}

// NewEngine creates a new gomap pattern Engine with the default '/' shortcut prefix.
func NewEngine() *Engine {
	return &Engine{Wildcard: DefaultWildcard}
}

// Plan holds the compiled AST expressions and variable binding slot count.
type Plan struct {
	Expressions [][]*Node
	NumSlots    []int
}

// StatsResult holds exact arbitrary-precision word count and byte size metrics.
type StatsResult struct {
	TotalBytes *big.Int
	TotalWords *big.Int
	StartIndex *big.Int // Global 0-based index of first generated word
}

func countFileTokens(tokens []Token) int {
	count := 0
	for _, tok := range tokens {
		switch tok.Kind {
		case TokenFile:
			count++
		case TokenBindDef:
			count += countFileTokens(tok.Inner)
		}
	}
	return count
}

type slotAllocator struct {
	slots map[string]int
	next  int
}

func newSlotAllocator() *slotAllocator {
	return &slotAllocator{slots: make(map[string]int)}
}

func (s *slotAllocator) define(name string) int {
	if id, ok := s.slots[name]; ok {
		return id
	}
	id := s.next
	s.slots[name] = id
	s.next++
	return id
}

func (s *slotAllocator) lookup(name string) (int, bool) {
	id, ok := s.slots[name]
	return id, ok
}

func parseTokens(tokens []Token, fileGroups [][]string, fileIdx int, alloc *slotAllocator) ([]*Node, int, error) {
	nodes := make([]*Node, 0, len(tokens))
	n := len(tokens)

	for i := 0; i < n; i++ {
		tok := tokens[i]
		if tok.Kind == TokenQMark || tok.Kind == TokenBraces {
			return nil, fileIdx, NewPatternError(fmt.Sprintf("Unexpected quantifier token %s without preceding element", tok.Kind))
		}

		minRep, maxRep := 1, 1
		if i+1 < n {
			next := tokens[i+1]
			switch next.Kind {
			case TokenQMark:
				minRep, maxRep = 0, 1
				i++
			case TokenBraces:
				minRep, maxRep = next.MinRep, next.MaxRep
				i++
			}
		}

		switch tok.Kind {
		case TokenLit:
			if len(nodes) > 0 {
				prev := nodes[len(nodes)-1]
				if prev.Kind == NodeClass && prev.MinRep == 1 && prev.MaxRep == 1 && len(prev.Choices) == 1 && minRep == 1 && maxRep == 1 {
					merged := prev.Choices[0] + tok.Lit
					prev.Choices[0] = merged
					prev.ChoiceBytes[0] = []byte(merged)
					prev.SumBytes = uint64(len(prev.ChoiceBytes[0]))
					prev.SumRunes = uint64(utf8.RuneCountInString(merged))
					prev.cachedCard = nil
					continue
				}
			}
			nodes = append(nodes, NewClassNode([]string{tok.Lit}, minRep, maxRep))

		case TokenClass:
			nodes = append(nodes, NewClassNode(tok.Choices, minRep, maxRep))

		case TokenRange:
			nodes = append(nodes, NewRangeNode(tok.Range, tok.Choices, minRep, maxRep))

		case TokenFile:
			if fileIdx >= len(fileGroups) {
				return nil, fileIdx, NewPatternError("Insufficient file assignments")
			}
			nodes = append(nodes, NewFileNode(fileGroups[fileIdx], minRep, maxRep))
			fileIdx++

		case TokenBindDef:
			innerNodes, nextFileIdx, err := parseTokens(tok.Inner, fileGroups, fileIdx, alloc)
			if err != nil {
				return nil, fileIdx, err
			}
			fileIdx = nextFileIdx
			slot := alloc.define(tok.Name)
			nodes = append(nodes, &Node{
				Kind:   NodeBindDef,
				Name:   tok.Name,
				Slot:   slot,
				Inner:  innerNodes,
				MinRep: minRep,
				MaxRep: maxRep,
			})

		case TokenBindRef:
			slot, ok := alloc.lookup(tok.Name)
			if !ok {
				return nil, fileIdx, NewPatternError(fmt.Sprintf("Undefined variable %q", tok.Name))
			}
			nodes = append(nodes, &Node{
				Kind:   NodeBindRef,
				Name:   tok.Name,
				Slot:   slot,
				MinRep: minRep,
				MaxRep: maxRep,
			})

		default:
			return nil, fileIdx, NewPatternError(fmt.Sprintf("Unexpected token %s", tok.Kind))
		}
	}

	return nodes, fileIdx, nil
}

// Parse transforms tokenized expressions and file assignments into a compiled Plan.
func (g *Engine) Parse(tokensMulti [][]Token, files []string) (*Plan, error) {
	plan := &Plan{
		Expressions: make([][]*Node, 0, len(tokensMulti)),
		NumSlots:    make([]int, 0, len(tokensMulti)),
	}
	fileIdx := 0

	for _, tokens := range tokensMulti {
		countFT := countFileTokens(tokens)
		var fileGroups [][]string
		if countFT > 0 {
			avail := len(files) - fileIdx
			if avail < countFT {
				return nil, NewPatternError(fmt.Sprintf("Pattern requires %d files but %d were provided", countFT, avail))
			}
			fileGroups = make([][]string, countFT)
			for k := 0; k < countFT; k++ {
				fileGroups[k] = []string{files[fileIdx+k]}
			}
			fileIdx += countFT
		}

		alloc := newSlotAllocator()
		exprNodes, _, err := parseTokens(tokens, fileGroups, 0, alloc)
		if err != nil {
			return nil, err
		}
		plan.Expressions = append(plan.Expressions, exprNodes)
		plan.NumSlots = append(plan.NumSlots, alloc.next)
	}

	return plan, nil
}

// Compile is a convenience method that runs Tokenize followed by Parse.
func (g *Engine) Compile(pattern string, files []string) (*Plan, error) {
	toks, err := g.Tokenize(pattern)
	if err != nil {
		return nil, err
	}
	return g.Parse(toks, files)
}

type bindStatMeta struct {
	defCount    *big.Int
	defSumBytes *big.Int
	concreteLen int // >= 0 when fixed to a concrete bound value during prefix seeking
}

// statsSingle computes exact (totalBytes, totalWords) for a single expression's node list.
func statsSingle(nodes []*Node, delimLen int, inherited map[int]bindStatMeta) (*big.Int, *big.Int, error) {
	totalWords := big.NewInt(1)
	totalBytes := big.NewInt(0)

	meta := make(map[int]bindStatMeta, len(inherited)+2)
	for k, v := range inherited {
		meta[k] = v
	}

	for _, node := range nodes {
		var nodeCount, nodeBytes *big.Int

		switch node.Kind {
		case NodeClass:
			nodeCount, nodeBytes = sumPowerRepetitions(
				uint64(len(node.Choices)),
				new(big.Int).SetUint64(node.SumBytes),
				node.MinRep,
				node.MaxRep,
			)

		case NodeRange:
			nodeCount, nodeBytes = sumPowerRepetitions(
				node.Range.Count,
				rangeSumBytes(node.Range),
				node.MinRep,
				node.MaxRep,
			)

		case NodeFile:
			lines, err := node.LoadLines()
			if err != nil {
				return nil, nil, err
			}
			nodeCount, nodeBytes = sumPowerRepetitions(
				uint64(len(lines)),
				new(big.Int).SetUint64(node.linesSum),
				node.MinRep,
				node.MaxRep,
			)

		case NodeBindDef:
			innerBytes, innerCount, err := statsSingle(node.Inner, 0, meta)
			if err != nil {
				return nil, nil, err
			}
			nodeCount = big.NewInt(0)
			nodeBytes = big.NewInt(0)
			for r := node.MinRep; r <= node.MaxRep; r++ {
				if r == 0 {
					nodeCount.Add(nodeCount, big.NewInt(1))
				} else {
					nodeCount.Add(nodeCount, innerCount)
					term := new(big.Int).Mul(innerBytes, big.NewInt(int64(r)))
					nodeBytes.Add(nodeBytes, term)
				}
			}
			meta[node.Slot] = bindStatMeta{
				defCount:    new(big.Int).Set(nodeCount),
				defSumBytes: new(big.Int).Set(nodeBytes),
				concreteLen: -1,
			}

		case NodeBindRef:
			bm, ok := meta[node.Slot]
			if !ok {
				return nil, nil, NewPatternError(fmt.Sprintf("Undefined variable %q", node.Name))
			}
			refCount := int64(node.MaxRep - node.MinRep + 1)
			var sumR int64
			for r := node.MinRep; r <= node.MaxRep; r++ {
				sumR += int64(r)
			}

			if bm.concreteLen >= 0 {
				// Concrete value bound during prefix-seeking suffix evaluation.
				nodeCount = big.NewInt(refCount)
				nodeBytes = big.NewInt(sumR * int64(bm.concreteLen))
			} else {
				// Exact combinatorics across all cycles of BindDef without integer division truncation:
				// newWords = totalWords * refCount
				// newBytes = totalBytes * refCount + (totalWords / defCount) * sumR * defSumBytes
				rcBig := big.NewInt(refCount)
				newBytes := new(big.Int).Mul(totalBytes, rcBig)
				if bm.defCount.Sign() > 0 && sumR > 0 && bm.defSumBytes.Sign() > 0 {
					cycles := new(big.Int).Quo(totalWords, bm.defCount)
					contrib := new(big.Int).Mul(cycles, big.NewInt(sumR))
					contrib.Mul(contrib, bm.defSumBytes)
					newBytes.Add(newBytes, contrib)
				}
				totalBytes = newBytes
				totalWords.Mul(totalWords, rcBig)
				continue
			}
		}

		// Standard Cartesian combination update:
		// totalBytes = totalBytes * nodeCount + nodeBytes * totalWords
		// totalWords = totalWords * nodeCount
		left := new(big.Int).Mul(totalBytes, nodeCount)
		right := new(big.Int).Mul(nodeBytes, totalWords)
		totalBytes = left.Add(left, right)
		totalWords.Mul(totalWords, nodeCount)
	}

	if delimLen > 0 && totalWords.Sign() > 0 {
		delimTotal := new(big.Int).Mul(totalWords, big.NewInt(int64(delimLen)))
		totalBytes.Add(totalBytes, delimTotal)
	}

	return totalBytes, totalWords, nil
}

func unreachableError(token string) error {
	return NewPatternError(fmt.Sprintf("Pattern does not produce word %q", token))
}

// rangePrefixIndex checks if s equals some element of spec and returns its 0-based index.
func rangePrefixIndex(spec RangeSpec, s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil || val < 0 {
		return 0, false
	}
	var idx uint64
	if spec.Step > 0 {
		if val < spec.Start || val > spec.End || (val-spec.Start)%spec.Step != 0 {
			return 0, false
		}
		idx = uint64((val - spec.Start) / spec.Step)
	} else {
		absStep := -spec.Step
		if val > spec.Start || val < spec.End || (spec.Start-val)%absStep != 0 {
			return 0, false
		}
		idx = uint64((spec.Start - val) / absStep)
	}
	if spec.FormatItem(idx) != s {
		return 0, false
	}
	return idx, true
}

// calculateSkippedInNodes finds the exact (skippedWords, skippedBytes, found) strictly before target
// using prefix pruning + cross-node backtracking so variable-width tokens always resolve accurately.
func calculateSkippedInNodes(
	nodes []*Node,
	idx int,
	remTarget string,
	prefixLen int64,
	bindings map[int]string,
	meta map[int]bindStatMeta,
) (*big.Int, *big.Int, bool, error) {
	if idx >= len(nodes) {
		if remTarget == "" {
			return big.NewInt(0), big.NewInt(0), true, nil
		}
		return big.NewInt(0), big.NewInt(0), false, nil
	}

	node := nodes[idx]

	switch node.Kind {
	case NodeBindDef:
		innerBytes, innerWords, err := statsSingle(node.Inner, 0, meta)
		if err != nil {
			return nil, nil, false, err
		}

		meta0 := make(map[int]bindStatMeta, len(meta)+1)
		meta1 := make(map[int]bindStatMeta, len(meta)+1)
		for k, v := range meta {
			meta0[k] = v
			meta1[k] = v
		}
		meta0[node.Slot] = bindStatMeta{concreteLen: 0}
		meta1[node.Slot] = bindStatMeta{concreteLen: 1}

		sufBytesZero, sufWords, err := statsSingle(nodes[idx+1:], 0, meta0)
		if err != nil {
			return nil, nil, false, err
		}
		sufBytesOne, _, err := statsSingle(nodes[idx+1:], 0, meta1)
		if err != nil {
			return nil, nil, false, err
		}

		// Suffix bytes scale linearly with bound byte length L: sufBytes(L) = sufBytesZero + L * slope.
		slope := new(big.Int).Sub(sufBytesOne, sufBytesZero)
		constPerItem := new(big.Int).Mul(sufWords, big.NewInt(prefixLen))
		constPerItem.Add(constPerItem, sufBytesZero)
		varFactor := new(big.Int).Add(sufWords, slope)

		calcBatchSkip := func(cntW, sumInnerB *big.Int, r int) (*big.Int, *big.Int) {
			wSkip := new(big.Int).Mul(cntW, sufWords)
			bSkip := new(big.Int).Mul(cntW, constPerItem)
			if r > 0 && sumInnerB.Sign() > 0 {
				sumValB := new(big.Int).Mul(sumInnerB, big.NewInt(int64(r)))
				bSkip.Add(bSkip, new(big.Int).Mul(sumValB, varFactor))
			}
			return wSkip, bSkip
		}

		skippedWords := big.NewInt(0)
		skippedBytes := big.NewInt(0)

		type bindCand struct {
			cand string
			inW  *big.Int
			inB  *big.Int
		}

		for r := node.MinRep; r <= node.MaxRep; r++ {
			if r == 0 {
				nextBind := make(map[int]string, len(bindings)+1)
				for k, v := range bindings {
					nextBind[k] = v
				}
				nextBind[node.Slot] = ""

				subW, subB, ok, err := calculateSkippedInNodes(nodes, idx+1, remTarget, prefixLen, nextBind, meta0)
				if err != nil {
					return nil, nil, false, err
				}
				if ok {
					skippedWords.Add(skippedWords, subW)
					skippedBytes.Add(skippedBytes, subB)
					return skippedWords, skippedBytes, true, nil
				}
				wSkip, bSkip := calcBatchSkip(big.NewInt(1), big.NewInt(0), 0)
				skippedWords.Add(skippedWords, wSkip)
				skippedBytes.Add(skippedBytes, bSkip)
				continue
			}

			// Only prefixes cand = remTarget[:m] (0 <= m <= len(remTarget)/r) where remTarget starts
			// with strings.Repeat(cand, r) can ever match remTarget: at most len(remTarget)+1 checks!
			maxM := len(remTarget) / r
			var cands []bindCand
			for m := 0; m <= maxM; m++ {
				cand := remTarget[:m]
				if !utf8.ValidString(cand) {
					continue
				}
				rep := strings.Repeat(cand, r)
				if !strings.HasPrefix(remTarget, rep) {
					continue
				}
				inW, inB, found, err := calculateSkippedInNodes(node.Inner, 0, cand, 0, bindings, meta)
				if err != nil {
					return nil, nil, false, err
				}
				if found {
					cands = append(cands, bindCand{cand: cand, inW: inW, inB: inB})
				}
			}

			// Sort matched candidates by inner generation order (inW ascending).
			for a := 1; a < len(cands); a++ {
				for b := a; b > 0 && cands[b].inW.Cmp(cands[b-1].inW) < 0; b-- {
					cands[b], cands[b-1] = cands[b-1], cands[b]
				}
			}

			for _, c := range cands {
				repVal := strings.Repeat(c.cand, r)
				nextMeta := make(map[int]bindStatMeta, len(meta)+1)
				for k, v := range meta {
					nextMeta[k] = v
				}
				nextMeta[node.Slot] = bindStatMeta{concreteLen: len(repVal)}

				nextBind := make(map[int]string, len(bindings)+1)
				for k, v := range bindings {
					nextBind[k] = v
				}
				nextBind[node.Slot] = repVal

				subW, subB, ok, err := calculateSkippedInNodes(
					nodes, idx+1, remTarget[len(repVal):], prefixLen+int64(len(repVal)), nextBind, nextMeta,
				)
				if err != nil {
					return nil, nil, false, err
				}
				if ok {
					wBefore, bBefore := calcBatchSkip(c.inW, c.inB, r)
					skippedWords.Add(skippedWords, wBefore)
					skippedWords.Add(skippedWords, subW)
					skippedBytes.Add(skippedBytes, bBefore)
					skippedBytes.Add(skippedBytes, subB)
					return skippedWords, skippedBytes, true, nil
				}
			}

			wBlock, bBlock := calcBatchSkip(innerWords, innerBytes, r)
			skippedWords.Add(skippedWords, wBlock)
			skippedBytes.Add(skippedBytes, bBlock)
		}
		return skippedWords, skippedBytes, false, nil

	case NodeBindRef:
		val, ok := bindings[node.Slot]
		if !ok {
			return nil, nil, false, NewPatternError(fmt.Sprintf("Undefined variable %q", node.Name))
		}
		sufBytes, sufWords, err := statsSingle(nodes[idx+1:], 0, meta)
		if err != nil {
			return nil, nil, false, err
		}

		skippedWords := big.NewInt(0)
		skippedBytes := big.NewInt(0)

		for r := node.MinRep; r <= node.MaxRep; r++ {
			out := strings.Repeat(val, r)
			if strings.HasPrefix(remTarget, out) {
				subW, subB, found, err := calculateSkippedInNodes(
					nodes, idx+1, remTarget[len(out):], prefixLen+int64(len(out)), bindings, meta,
				)
				if err != nil {
					return nil, nil, false, err
				}
				if found {
					skippedWords.Add(skippedWords, subW)
					skippedBytes.Add(skippedBytes, subB)
					return skippedWords, skippedBytes, true, nil
				}
			}

			skippedWords.Add(skippedWords, sufWords)
			branchBytes := new(big.Int).Mul(sufWords, big.NewInt(prefixLen+int64(len(out))))
			branchBytes.Add(branchBytes, sufBytes)
			skippedBytes.Add(skippedBytes, branchBytes)
		}
		return skippedWords, skippedBytes, false, nil

	default:
		// Pool node: NodeClass, NodeRange, or NodeFile.
		sufBytes, sufWords, err := statsSingle(nodes[idx+1:], 0, meta)
		if err != nil {
			return nil, nil, false, err
		}

		if node.Kind == NodeRange {
			return skipRangeNode(node, nodes, idx, remTarget, prefixLen, bindings, meta, sufWords, sufBytes)
		}

		var pool []string
		var poolSum uint64
		if node.Kind == NodeFile {
			lines, err := node.LoadLines()
			if err != nil {
				return nil, nil, false, err
			}
			pool = lines
			poolSum = node.linesSum
		} else {
			pool = node.Choices
			poolSum = node.SumBytes
		}

		return skipPoolSlice(pool, poolSum, node.MinRep, node.MaxRep, nodes, idx, remTarget, prefixLen, bindings, meta, sufWords, sufBytes)
	}
}

func skipPoolSlice(
	pool []string,
	poolSum uint64,
	minRep, maxRep int,
	nodes []*Node,
	idx int,
	remTarget string,
	prefixLen int64,
	bindings map[int]string,
	meta map[int]bindStatMeta,
	sufWords, sufBytes *big.Int,
) (*big.Int, *big.Int, bool, error) {
	skippedWords := big.NewInt(0)
	skippedBytes := big.NewInt(0)
	bigK := new(big.Int).SetUint64(uint64(len(pool)))
	bigSum := new(big.Int).SetUint64(poolSum)

	var recDepth func(depth int, curRem string, curPrefix int64) (*big.Int, *big.Int, bool, error)
	recDepth = func(depth int, curRem string, curPrefix int64) (*big.Int, *big.Int, bool, error) {
		if depth == 0 {
			return calculateSkippedInNodes(nodes, idx+1, curRem, curPrefix, bindings, meta)
		}

		dSkippedW := big.NewInt(0)
		dSkippedB := big.NewInt(0)

		kRem := new(big.Int).Exp(bigK, big.NewInt(int64(depth-1)), nil)
		// Subtree word count per item = k^(depth-1) * sufWords
		itemWords := new(big.Int).Mul(kRem, sufWords)

		// Internal pool bytes across remaining depth-1 positions (for 1 suffix):
		// (depth-1) * k^(depth-2) * poolSum
		innerPoolBytes := big.NewInt(0)
		if depth-1 > 0 && len(pool) > 0 {
			kPrev := new(big.Int).Exp(bigK, big.NewInt(int64(depth-2)), nil)
			innerPoolBytes.Mul(kPrev, big.NewInt(int64(depth-1)))
			innerPoolBytes.Mul(innerPoolBytes, bigSum)
		}

		for _, item := range pool {
			itemLen := int64(len(item))
			if strings.HasPrefix(curRem, item) {
				subW, subB, ok, err := recDepth(depth-1, curRem[len(item):], curPrefix+itemLen)
				if err != nil {
					return nil, nil, false, err
				}
				if ok {
					dSkippedW.Add(dSkippedW, subW)
					dSkippedB.Add(dSkippedB, subB)
					return dSkippedW, dSkippedB, true, nil
				}
			}

			dSkippedW.Add(dSkippedW, itemWords)

			// Subtree bytes = sufWords * (innerPoolBytes + k^(depth-1)*(curPrefix + itemLen)) + k^(depth-1)*sufBytes
			prefixPart := new(big.Int).Mul(kRem, big.NewInt(curPrefix+itemLen))
			prefixPart.Add(prefixPart, innerPoolBytes)
			prefixPart.Mul(prefixPart, sufWords)
			sufPart := new(big.Int).Mul(kRem, sufBytes)
			prefixPart.Add(prefixPart, sufPart)
			dSkippedB.Add(dSkippedB, prefixPart)
		}

		return dSkippedW, dSkippedB, false, nil
	}

	for r := minRep; r <= maxRep; r++ {
		if r == 0 {
			subW, subB, ok, err := calculateSkippedInNodes(nodes, idx+1, remTarget, prefixLen, bindings, meta)
			if err != nil {
				return nil, nil, false, err
			}
			if ok {
				skippedWords.Add(skippedWords, subW)
				skippedBytes.Add(skippedBytes, subB)
				return skippedWords, skippedBytes, true, nil
			}
			skippedWords.Add(skippedWords, sufWords)
			b := new(big.Int).Mul(sufWords, big.NewInt(prefixLen))
			b.Add(b, sufBytes)
			skippedBytes.Add(skippedBytes, b)
			continue
		}

		subW, subB, ok, err := recDepth(r, remTarget, prefixLen)
		if err != nil {
			return nil, nil, false, err
		}
		if ok {
			skippedWords.Add(skippedWords, subW)
			skippedBytes.Add(skippedBytes, subB)
			return skippedWords, skippedBytes, true, nil
		}
		skippedWords.Add(skippedWords, subW)
		skippedBytes.Add(skippedBytes, subB)
	}

	return skippedWords, skippedBytes, false, nil
}

// skipRangeNode performs O(len(target)^r) arithmetic prefix seeking on huge unmaterialized ranges.
func skipRangeNode(
	node *Node,
	nodes []*Node,
	idx int,
	remTarget string,
	prefixLen int64,
	bindings map[int]string,
	meta map[int]bindStatMeta,
	sufWords, sufBytes *big.Int,
) (*big.Int, *big.Int, bool, error) {
	spec := node.Range
	bigK := new(big.Int).SetUint64(spec.Count)
	bigSum := rangeSumBytes(spec)

	skippedWords := big.NewInt(0)
	skippedBytes := big.NewInt(0)

	type candMatch struct {
		idx uint64
		str string
	}

	var recDepth func(depth int, curRem string, curPrefix int64) (*big.Int, *big.Int, bool, error)
	recDepth = func(depth int, curRem string, curPrefix int64) (*big.Int, *big.Int, bool, error) {
		if depth == 0 {
			return calculateSkippedInNodes(nodes, idx+1, curRem, curPrefix, bindings, meta)
		}

		// Collect at most 20 candidate prefix matches from curRem, sorted by range index!
		maxL := len(curRem)
		if maxL > 20 {
			maxL = 20
		}
		var cands []candMatch
		for l := 1; l <= maxL; l++ {
			if cIdx, ok := rangePrefixIndex(spec, curRem[:l]); ok {
				cands = append(cands, candMatch{idx: cIdx, str: curRem[:l]})
			}
		}
		// Sort cands by idx ascending (at most 20 items: insertion sort).
		for a := 1; a < len(cands); a++ {
			for b := a; b > 0 && cands[b].idx < cands[b-1].idx; b-- {
				cands[b], cands[b-1] = cands[b-1], cands[b]
			}
		}

		kRem := new(big.Int).Exp(bigK, big.NewInt(int64(depth-1)), nil)
		innerPoolBytes := big.NewInt(0)
		if depth-1 > 0 && spec.Count > 0 {
			kPrev := new(big.Int).Exp(bigK, big.NewInt(int64(depth-2)), nil)
			innerPoolBytes.Mul(kPrev, big.NewInt(int64(depth-1)))
			innerPoolBytes.Mul(innerPoolBytes, bigSum)
		}

		// Helper to add skipped block of [loIdx, hiIdx) items in O(1)!
		addBlock := func(wAcc, bAcc *big.Int, loIdx, hiIdx uint64) {
			if hiIdx <= loIdx {
				return
			}
			cnt := hiIdx - loIdx
			subSpec := RangeSpec{
				Start:    spec.Start + int64(loIdx)*spec.Step,
				End:      spec.Start + int64(hiIdx-1)*spec.Step,
				Step:     spec.Step,
				PadWidth: spec.PadWidth,
				Count:    cnt,
			}
			blockItemBytes := rangeSumBytes(subSpec)
			bigCnt := new(big.Int).SetUint64(cnt)

			// Words = cnt * kRem * sufWords
			wTerm := new(big.Int).Mul(bigCnt, kRem)
			wTerm.Mul(wTerm, sufWords)
			wAcc.Add(wAcc, wTerm)

			// Bytes = cnt * (sufWords*innerPoolBytes + kRem*sufBytes) + sufWords*kRem*(cnt*curPrefix + blockItemBytes)
			perItemFixed := new(big.Int).Mul(sufWords, innerPoolBytes)
			perItemFixed.Add(perItemFixed, new(big.Int).Mul(kRem, sufBytes))
			bTerm1 := new(big.Int).Mul(bigCnt, perItemFixed)

			prefSum := new(big.Int).Mul(bigCnt, big.NewInt(curPrefix))
			prefSum.Add(prefSum, blockItemBytes)
			bTerm2 := new(big.Int).Mul(prefSum, kRem)
			bTerm2.Mul(bTerm2, sufWords)

			bAcc.Add(bAcc, bTerm1)
			bAcc.Add(bAcc, bTerm2)
		}

		dSkippedW := big.NewInt(0)
		dSkippedB := big.NewInt(0)
		var cursor uint64

		for _, c := range cands {
			if c.idx < cursor {
				continue
			}
			addBlock(dSkippedW, dSkippedB, cursor, c.idx)

			subW, subB, ok, err := recDepth(depth-1, curRem[len(c.str):], curPrefix+int64(len(c.str)))
			if err != nil {
				return nil, nil, false, err
			}
			if ok {
				dSkippedW.Add(dSkippedW, subW)
				dSkippedB.Add(dSkippedB, subB)
				return dSkippedW, dSkippedB, true, nil
			}
			// Failed candidate: skip [c.idx, c.idx+1)
			addBlock(dSkippedW, dSkippedB, c.idx, c.idx+1)
			cursor = c.idx + 1
		}

		addBlock(dSkippedW, dSkippedB, cursor, spec.Count)
		return dSkippedW, dSkippedB, false, nil
	}

	for r := node.MinRep; r <= node.MaxRep; r++ {
		if r == 0 {
			subW, subB, ok, err := calculateSkippedInNodes(nodes, idx+1, remTarget, prefixLen, bindings, meta)
			if err != nil {
				return nil, nil, false, err
			}
			if ok {
				skippedWords.Add(skippedWords, subW)
				skippedBytes.Add(skippedBytes, subB)
				return skippedWords, skippedBytes, true, nil
			}
			skippedWords.Add(skippedWords, sufWords)
			b := new(big.Int).Mul(sufWords, big.NewInt(prefixLen))
			b.Add(b, sufBytes)
			skippedBytes.Add(skippedBytes, b)
			continue
		}

		subW, subB, ok, err := recDepth(r, remTarget, prefixLen)
		if err != nil {
			return nil, nil, false, err
		}
		if ok {
			skippedWords.Add(skippedWords, subW)
			skippedBytes.Add(skippedBytes, subB)
			return skippedWords, skippedBytes, true, nil
		}
		skippedWords.Add(skippedWords, subW)
		skippedBytes.Add(skippedBytes, subB)
	}

	return skippedWords, skippedBytes, false, nil
}

// CalculateSkippedStats returns (skippedWords, skippedBytes) strictly before target across all expressions.
func (g *Engine) CalculateSkippedStats(plan *Plan, target string) (*big.Int, *big.Int, error) {
	skippedWords := big.NewInt(0)
	skippedBytes := big.NewInt(0)

	for _, exprNodes := range plan.Expressions {
		w, b, found, err := calculateSkippedInNodes(exprNodes, 0, target, 0, nil, nil)
		if err != nil {
			return nil, nil, err
		}
		if found {
			skippedWords.Add(skippedWords, w)
			skippedBytes.Add(skippedBytes, b)
			return skippedWords, skippedBytes, nil
		}
		fullB, fullW, err := statsSingle(exprNodes, 0, nil)
		if err != nil {
			return nil, nil, err
		}
		skippedWords.Add(skippedWords, fullW)
		skippedBytes.Add(skippedBytes, fullB)
	}

	return nil, nil, unreachableError(target)
}

// Stats computes exact total bytes, total words, and starting global index for a compiled Plan,
// taking optional startToken and endToken bounds into account.
func (g *Engine) Stats(plan *Plan, delimLen int, startToken, endToken string, hasStart, hasEnd bool) (StatsResult, error) {
	actualWords := big.NewInt(0)
	actualBytes := big.NewInt(0)
	startIndex := big.NewInt(0)

	startFound := !hasStart
	endFound := false
	bigDelim := big.NewInt(int64(delimLen))

	for _, exprNodes := range plan.Expressions {
		fullB, fullW, err := statsSingle(exprNodes, delimLen, nil)
		if err != nil {
			return StatsResult{}, err
		}

		exprStartW := big.NewInt(0)
		exprStartB := big.NewInt(0)
		exprEndW := new(big.Int).Set(fullW)
		exprEndB := new(big.Int).Set(fullB)

		if !startFound {
			w, b, found, err := calculateSkippedInNodes(exprNodes, 0, startToken, 0, nil, nil)
			if err != nil {
				return StatsResult{}, err
			}
			if !found {
				startIndex.Add(startIndex, fullW)
				continue
			}
			exprStartW = w
			exprStartB = new(big.Int).Mul(w, bigDelim)
			exprStartB.Add(exprStartB, b)
			startIndex.Add(startIndex, w)
			startFound = true
		}

		if startFound && !endFound && hasEnd {
			w, b, found, err := calculateSkippedInNodes(exprNodes, 0, endToken, 0, nil, nil)
			if err != nil {
				return StatsResult{}, err
			}
			if found && w.Cmp(exprStartW) >= 0 {
				endPlusOne := new(big.Int).Add(w, big.NewInt(1))
				exprEndW = endPlusOne
				exprEndB = new(big.Int).Mul(w, bigDelim)
				exprEndB.Add(exprEndB, b)
				exprEndB.Add(exprEndB, big.NewInt(int64(len(endToken)+delimLen)))
				endFound = true
			}
		}

		if startFound {
			cnt := new(big.Int).Sub(exprEndW, exprStartW)
			bts := new(big.Int).Sub(exprEndB, exprStartB)
			if cnt.Sign() > 0 {
				actualWords.Add(actualWords, cnt)
				actualBytes.Add(actualBytes, bts)
			}
		}

		if endFound {
			break
		}
	}

	if hasStart && !startFound {
		return StatsResult{}, unreachableError(startToken)
	}
	if hasEnd && !endFound {
		// Check if endToken existed in the pattern at all (e.g. strictly before startToken).
		if _, _, err := g.CalculateSkippedStats(plan, endToken); err != nil {
			return StatsResult{}, unreachableError(endToken)
		}
		// endToken exists in pattern, but strictly before startToken -> 0 entries.
		return StatsResult{
			TotalBytes: big.NewInt(0),
			TotalWords: big.NewInt(0),
			StartIndex: startIndex,
		}, nil
	}

	return StatsResult{
		TotalBytes: actualBytes,
		TotalWords: actualWords,
		StartIndex: startIndex,
	}, nil
}

// runesSingle computes exact (totalRunes, totalWords) in Unicode characters for a single expression.
func runesSingle(nodes []*Node, inherited map[int]bindStatMeta) (*big.Int, *big.Int, error) {
	totalWords := big.NewInt(1)
	totalRunes := big.NewInt(0)

	meta := make(map[int]bindStatMeta, len(inherited)+2)
	for k, v := range inherited {
		meta[k] = v
	}

	for _, node := range nodes {
		var nodeCount, nodeRunes *big.Int

		switch node.Kind {
		case NodeClass:
			nodeCount, nodeRunes = sumPowerRepetitions(
				uint64(len(node.Choices)),
				new(big.Int).SetUint64(node.SumRunes),
				node.MinRep,
				node.MaxRep,
			)
		case NodeRange:
			nodeCount, nodeRunes = sumPowerRepetitions(
				node.Range.Count,
				rangeSumBytes(node.Range),
				node.MinRep,
				node.MaxRep,
			)
		case NodeFile:
			lines, err := node.LoadLines()
			if err != nil {
				return nil, nil, err
			}
			nodeCount, nodeRunes = sumPowerRepetitions(
				uint64(len(lines)),
				new(big.Int).SetUint64(node.linesRunes),
				node.MinRep,
				node.MaxRep,
			)
		case NodeBindDef:
			innerRunes, innerCount, err := runesSingle(node.Inner, meta)
			if err != nil {
				return nil, nil, err
			}
			nodeCount = big.NewInt(0)
			nodeRunes = big.NewInt(0)
			for r := node.MinRep; r <= node.MaxRep; r++ {
				if r == 0 {
					nodeCount.Add(nodeCount, big.NewInt(1))
				} else {
					nodeCount.Add(nodeCount, innerCount)
					term := new(big.Int).Mul(innerRunes, big.NewInt(int64(r)))
					nodeRunes.Add(nodeRunes, term)
				}
			}
			meta[node.Slot] = bindStatMeta{
				defCount:    new(big.Int).Set(nodeCount),
				defSumBytes: new(big.Int).Set(nodeRunes),
				concreteLen: -1,
			}
		case NodeBindRef:
			bm, ok := meta[node.Slot]
			if !ok {
				return nil, nil, NewPatternError(fmt.Sprintf("Undefined variable %q", node.Name))
			}
			refCount := int64(node.MaxRep - node.MinRep + 1)
			var sumR int64
			for r := node.MinRep; r <= node.MaxRep; r++ {
				sumR += int64(r)
			}
			rcBig := big.NewInt(refCount)
			newRunes := new(big.Int).Mul(totalRunes, rcBig)
			if bm.defCount.Sign() > 0 && sumR > 0 && bm.defSumBytes.Sign() > 0 {
				cycles := new(big.Int).Quo(totalWords, bm.defCount)
				contrib := new(big.Int).Mul(cycles, big.NewInt(sumR))
				contrib.Mul(contrib, bm.defSumBytes)
				newRunes.Add(newRunes, contrib)
			}
			totalRunes = newRunes
			totalWords.Mul(totalWords, rcBig)
			continue
		}

		left := new(big.Int).Mul(totalRunes, nodeCount)
		right := new(big.Int).Mul(nodeRunes, totalWords)
		totalRunes = left.Add(left, right)
		totalWords.Mul(totalWords, nodeCount)
	}

	return totalRunes, totalWords, nil
}

// ExprSummary holds combinatorial metrics and representative sample words for one sub-expression.
type ExprSummary struct {
	Words      *big.Int
	AvgChars   float64
	HeadWords  []string
	TailWords  []string
	AllShown   bool
}

// InspectExpressions computes per-expression counts, average rune lengths, and O(1) boundary
// sample words (first/last entries, or all entries when <= 5) for visual preview tables.
func (g *Engine) InspectExpressions(plan *Plan) ([]ExprSummary, error) {
	summaries := make([]ExprSummary, len(plan.Expressions))
	five := big.NewInt(5)

	for i, exprNodes := range plan.Expressions {
		r, w, err := runesSingle(exprNodes, nil)
		if err != nil {
			return nil, err
		}
		var avg float64
		if w.Sign() > 0 {
			rf := new(big.Float).SetPrec(128).SetInt(r)
			wf := new(big.Float).SetPrec(128).SetInt(w)
			avg, _ = new(big.Float).Quo(rf, wf).Float64()
		}

		singlePlan := &Plan{
			Expressions: [][]*Node{exprNodes},
			NumSlots:    []int{plan.NumSlots[i]},
		}

		sum := ExprSummary{
			Words:    w,
			AvgChars: avg,
		}

		if w.Sign() == 0 {
			sum.AllShown = true
		} else if w.Cmp(five) <= 0 {
			n := int(w.Int64())
			sum.AllShown = true
			sum.HeadWords = make([]string, 0, n)
			for k := 0; k < n; k++ {
				word, err := g.WordAtIndex(singlePlan, big.NewInt(int64(k)))
				if err != nil {
					return nil, err
				}
				sum.HeadWords = append(sum.HeadWords, word)
			}
		} else {
			// Sample first 2 and last 2 combinations in O(1) via WordAtIndex.
			sum.HeadWords = make([]string, 0, 2)
			for k := int64(0); k < 2; k++ {
				word, err := g.WordAtIndex(singlePlan, big.NewInt(k))
				if err != nil {
					return nil, err
				}
				sum.HeadWords = append(sum.HeadWords, word)
			}
			sum.TailWords = make([]string, 0, 2)
			for k := int64(2); k >= 1; k-- {
				idx := new(big.Int).Sub(w, big.NewInt(k))
				word, err := g.WordAtIndex(singlePlan, idx)
				if err != nil {
					return nil, err
				}
				sum.TailWords = append(sum.TailWords, word)
			}
		}

	summaries[i] = sum
	}

	return summaries, nil
}

// AverageCharLengths computes both the overall weighted average length (in Unicode characters)
// across all generated entries and the unweighted mean of per-expression average lengths.
func (g *Engine) AverageCharLengths(plan *Plan) (weightedAvg float64, exprMeanAvg float64, err error) {
	totalRunes := big.NewInt(0)
	totalWords := big.NewInt(0)
	var sumExprAvg float64
	nonEmptyExprs := 0

	for _, exprNodes := range plan.Expressions {
		r, w, err := runesSingle(exprNodes, nil)
		if err != nil {
			return 0, 0, err
		}
		totalRunes.Add(totalRunes, r)
		totalWords.Add(totalWords, w)
		if w.Sign() > 0 {
			rf := new(big.Float).SetPrec(128).SetInt(r)
			wf := new(big.Float).SetPrec(128).SetInt(w)
			ea, _ := new(big.Float).Quo(rf, wf).Float64()
			sumExprAvg += ea
			nonEmptyExprs++
		}
	}

	if totalWords.Sign() > 0 {
		rf := new(big.Float).SetPrec(128).SetInt(totalRunes)
		wf := new(big.Float).SetPrec(128).SetInt(totalWords)
		weightedAvg, _ = new(big.Float).Quo(rf, wf).Float64()
	}
	if nonEmptyExprs > 0 {
		exprMeanAvg = sumExprAvg / float64(nonEmptyExprs)
	}
	return weightedAvg, exprMeanAvg, nil
}

// WordAtIndex retrieves the exact generated string at a global 0-based index across the Plan.
func (g *Engine) WordAtIndex(plan *Plan, index *big.Int) (string, error) {
	if index.Sign() < 0 {
		return "", NewPatternError("Index out of bounds")
	}
	rem := new(big.Int).Set(index)

	for i, exprNodes := range plan.Expressions {
		_, fullW, err := statsSingle(exprNodes, 0, nil)
		if err != nil {
			return "", err
		}
		if rem.Cmp(fullW) < 0 {
			var out string
			err := generateExprRange(exprNodes, plan.NumSlots[i], rem, 1, nil, 64, func(chunk []byte, _ uint64) error {
				out = string(chunk)
				return nil
			})
			if err != nil {
				return "", err
			}
			return out, nil
		}
		rem.Sub(rem, fullW)
	}

	return "", NewPatternError("Index out of bounds")
}
