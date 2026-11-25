package gomap

import (
	"bufio"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// NodeKind discriminates compiled AST node types.
type NodeKind uint8

const (
	NodeClass   NodeKind = iota // Literal, character class, union, or materialized small range
	NodeRange                   // Large arithmetic numeric range (> MaterializeThreshold)
	NodeFile                    // External wordlist file placeholder '^'
	NodeBindDef                 // Value binding definition <@name=expr>
	NodeBindRef                 // Value binding reference <@name>
)

// Node represents a compiled pattern element within an expression.
type Node struct {
	Kind   NodeKind
	MinRep int
	MaxRep int

	// Choices holds string items for NodeClass (and materialized NodeRange).
	Choices     []string
	ChoiceBytes [][]byte
	SumBytes    uint64 // Sum of UTF-8 byte lengths across Choices
	SumRunes    uint64 // Sum of Unicode rune lengths across Choices

	// Range holds arithmetic bounds for NodeRange.
	Range RangeSpec

	// Files holds file paths for NodeFile; Lines are loaded lazily and cached safely via sync.Once.
	Files       []string
	linesOnce   sync.Once
	cachedLines []string
	linesBytes  [][]byte
	linesSum    uint64
	linesRunes  uint64
	linesErr    error

	// Binding metadata for NodeBindDef and NodeBindRef.
	Name  string
	Slot  int
	Inner []*Node

	cardOnce   sync.Once
	cachedCard *big.Int
	cardErr    error
}

// NewClassNode creates a Node from one or more literal/class alternatives.
func NewClassNode(choices []string, minRep, maxRep int) *Node {
	cb := make([][]byte, len(choices))
	var sumB, sumR uint64
	for i, s := range choices {
		b := []byte(s)
		cb[i] = b
		sumB += uint64(len(b))
		sumR += uint64(utf8.RuneCountInString(s))
	}
	return &Node{
		Kind:        NodeClass,
		MinRep:      minRep,
		MaxRep:      maxRep,
		Choices:     choices,
		ChoiceBytes: cb,
		SumBytes:    sumB,
		SumRunes:    sumR,
	}
}

// NewRangeNode creates a Node from a parsed RangeSpec.
func NewRangeNode(spec RangeSpec, choices []string, minRep, maxRep int) *Node {
	if len(choices) > 0 {
		cb := make([][]byte, len(choices))
		var sum uint64
		for i, s := range choices {
			b := []byte(s)
			cb[i] = b
			sum += uint64(len(b))
		}
		return &Node{
			Kind:        NodeClass,
			MinRep:      minRep,
			MaxRep:      maxRep,
			Choices:     choices,
			ChoiceBytes: cb,
			SumBytes:    sum,
			SumRunes:    sum, // numeric range strings are pure ASCII
			Range:       spec,
		}
	}
	return &Node{
		Kind:   NodeRange,
		MinRep: minRep,
		MaxRep: maxRep,
		Range:  spec,
	}
}

// NewFileNode creates a Node backed by one or more external wordlist files.
func NewFileNode(files []string, minRep, maxRep int) *Node {
	return &Node{
		Kind:   NodeFile,
		MinRep: minRep,
		MaxRep: maxRep,
		Files:  files,
	}
}

// LoadLines loads and caches all lines from a FileNode's files in a concurrency-safe manner.
func (n *Node) LoadLines() ([]string, error) {
	n.linesOnce.Do(func() {
		var lines []string
		var sumB, sumR uint64
		for _, path := range n.Files {
			info, err := os.Stat(path)
			if err != nil {
				n.linesErr = NewPatternError(fmt.Sprintf("Failed to open or read file %q", path))
				return
			}
			if info.IsDir() {
				n.linesErr = NewPatternError(fmt.Sprintf("Failed to open or read file %q: is a directory", path))
				return
			}
			f, err := os.Open(path)
			if err != nil {
				n.linesErr = NewPatternError(fmt.Sprintf("Failed to open or read file %q", path))
				return
			}
			reader := bufio.NewReaderSize(f, 64*1024)
			for {
				line, readErr := reader.ReadString('\n')
				if len(line) > 0 {
					trimmed := strings.TrimRight(line, "\r\n")
					lines = append(lines, trimmed)
					sumB += uint64(len(trimmed))
					sumR += uint64(utf8.RuneCountInString(trimmed))
				}
				if readErr != nil {
					if readErr == io.EOF {
						break
					}
					_ = f.Close()
					n.linesErr = NewPatternError(fmt.Sprintf("Failed to open or read file %q", path))
					return
				}
			}
			_ = f.Close()
		}
		if len(lines) == 0 {
			n.linesErr = NewPatternError(fmt.Sprintf("No lines produced from files %v", n.Files))
			return
		}
		lb := make([][]byte, len(lines))
		for i, s := range lines {
			lb[i] = []byte(s)
		}
		n.cachedLines = lines
		n.linesBytes = lb
		n.linesSum = sumB
		n.linesRunes = sumR
	})
	if n.linesErr != nil {
		return nil, n.linesErr
	}
	return n.cachedLines, nil
}

// rangeSumBytes computes the exact sum of decimal string byte lengths across all items
// of a RangeSpec in O(log10(max)) time with zero memory allocations.
func rangeSumBytes(spec RangeSpec) *big.Int {
	if spec.Count == 0 {
		return big.NewInt(0)
	}
	// Normalize to ascending sequence: first + k * absStep for k in [0, Count-1].
	var first, absStep int64
	if spec.Step > 0 {
		first = spec.Start
		absStep = spec.Step
	} else {
		absStep = -spec.Step
		first = spec.Start - int64(spec.Count-1)*absStep
	}
	last := first + int64(spec.Count-1)*absStep

	total := new(big.Int)

	// Bucket 1-digit numbers: [0, 9]
	var pow10 [19]int64
	pow10[0] = 1
	for d := 1; d < 19; d++ {
		pow10[d] = pow10[d-1] * 10
	}

	for d := 1; d <= 19; d++ {
		var lo, hi int64
		if d == 1 {
			lo, hi = 0, 9
		} else if d < 19 {
			lo, hi = pow10[d-1], pow10[d]-1
		} else {
			lo, hi = pow10[18], last
		}
		if last < lo || first > hi {
			continue
		}

		var kMin, kMax uint64
		if first >= lo {
			kMin = 0
		} else {
			// Smallest k such that first + k*absStep >= lo
			kMin = uint64((lo - first + absStep - 1) / absStep)
		}
		if last <= hi {
			kMax = spec.Count - 1
		} else {
			kMax = uint64((hi - first) / absStep)
		}
		if kMin <= kMax {
			cnt := kMax - kMin + 1
			width := d
			if spec.PadWidth > width {
				width = spec.PadWidth
			}
			term := new(big.Int).SetUint64(cnt)
			term.Mul(term, big.NewInt(int64(width)))
			total.Add(total, term)
		}
	}
	return total
}

// sumPowerRepetitions calculates:
//
//	count = sum_{r=minRep}^{maxRep} (r == 0 ? 1 : k^r)
//	bytes = sum_{r=minRep}^{maxRep} (r == 0 ? 0 : r * k^(r-1) * sumLen)
func sumPowerRepetitions(k uint64, sumLen *big.Int, minRep, maxRep int) (*big.Int, *big.Int) {
	count := big.NewInt(0)
	bytesTotal := big.NewInt(0)
	if minRep == 0 && maxRep == 0 {
		return big.NewInt(1), big.NewInt(0)
	}
	bigK := new(big.Int).SetUint64(k)
	one := big.NewInt(1)

	for r := minRep; r <= maxRep; r++ {
		if r == 0 {
			count.Add(count, one)
			continue
		}
		if k == 0 {
			continue
		}
		// k^(r-1)
		kPrev := new(big.Int).Exp(bigK, big.NewInt(int64(r-1)), nil)
		// k^r = kPrev * k
		kCur := new(big.Int).Mul(kPrev, bigK)
		count.Add(count, kCur)

		// r * k^(r-1) * sumLen
		b := new(big.Int).Mul(kPrev, big.NewInt(int64(r)))
		b.Mul(b, sumLen)
		bytesTotal.Add(bytesTotal, b)
	}
	return count, bytesTotal
}

// Cardinality returns the exact number of combinations produced by this single Node (concurrency-safe).
func (n *Node) Cardinality() (*big.Int, error) {
	n.cardOnce.Do(func() {
		switch n.Kind {
		case NodeClass:
			c, _ := sumPowerRepetitions(uint64(len(n.Choices)), new(big.Int).SetUint64(n.SumBytes), n.MinRep, n.MaxRep)
			n.cachedCard = c

		case NodeRange:
			c, _ := sumPowerRepetitions(n.Range.Count, big.NewInt(0), n.MinRep, n.MaxRep)
			n.cachedCard = c

		case NodeFile:
			lines, err := n.LoadLines()
			if err != nil {
				n.cardErr = err
				return
			}
			c, _ := sumPowerRepetitions(uint64(len(lines)), new(big.Int).SetUint64(n.linesSum), n.MinRep, n.MaxRep)
			n.cachedCard = c

		case NodeBindDef:
			innerCard := big.NewInt(1)
			for _, in := range n.Inner {
				ic, err := in.Cardinality()
				if err != nil {
					n.cardErr = err
					return
				}
				innerCard.Mul(innerCard, ic)
			}
			card := big.NewInt(0)
			for r := n.MinRep; r <= n.MaxRep; r++ {
				if r == 0 {
					card.Add(card, big.NewInt(1))
				} else {
					card.Add(card, innerCard)
				}
			}
			n.cachedCard = card

		case NodeBindRef:
			n.cachedCard = big.NewInt(int64(n.MaxRep - n.MinRep + 1))
		}
	})
	if n.cardErr != nil {
		return nil, n.cardErr
	}
	return new(big.Int).Set(n.cachedCard), nil
}

// ItemAt retrieves the exact string item produced by a standalone pool Node at 0-based index idx.
func (n *Node) ItemAt(idx *big.Int) (string, error) {
	rem := new(big.Int).Set(idx)
	if rem.Sign() < 0 {
		return "", fmt.Errorf("index out of range")
	}

	switch n.Kind {
	case NodeClass:
		baseLen := uint64(len(n.Choices))
		bigBase := new(big.Int).SetUint64(baseLen)
		for r := n.MinRep; r <= n.MaxRep; r++ {
			var count *big.Int
			if r == 0 {
				count = big.NewInt(1)
			} else {
				count = new(big.Int).Exp(bigBase, big.NewInt(int64(r)), nil)
			}
			if rem.Cmp(count) < 0 {
				if r == 0 {
					return "", nil
				}
				indices := make([]uint64, r)
				tmp := new(big.Int).Set(rem)
				mod := new(big.Int)
				for k := r - 1; k >= 0; k-- {
					tmp.DivMod(tmp, bigBase, mod)
					indices[k] = mod.Uint64()
				}
				var sb strings.Builder
				for _, i := range indices {
					sb.WriteString(n.Choices[i])
				}
				return sb.String(), nil
			}
			rem.Sub(rem, count)
		}
		return "", fmt.Errorf("index out of range")

	case NodeRange:
		baseLen := n.Range.Count
		bigBase := new(big.Int).SetUint64(baseLen)
		for r := n.MinRep; r <= n.MaxRep; r++ {
			var count *big.Int
			if r == 0 {
				count = big.NewInt(1)
			} else {
				count = new(big.Int).Exp(bigBase, big.NewInt(int64(r)), nil)
			}
			if rem.Cmp(count) < 0 {
				if r == 0 {
					return "", nil
				}
				indices := make([]uint64, r)
				tmp := new(big.Int).Set(rem)
				mod := new(big.Int)
				for k := r - 1; k >= 0; k-- {
					tmp.DivMod(tmp, bigBase, mod)
					indices[k] = mod.Uint64()
				}
				var sb strings.Builder
				for _, i := range indices {
					sb.WriteString(n.Range.FormatItem(i))
				}
				return sb.String(), nil
			}
			rem.Sub(rem, count)
		}
		return "", fmt.Errorf("index out of range")

	case NodeFile:
		lines, err := n.LoadLines()
		if err != nil {
			return "", err
		}
		baseLen := uint64(len(lines))
		bigBase := new(big.Int).SetUint64(baseLen)
		for r := n.MinRep; r <= n.MaxRep; r++ {
			var count *big.Int
			if r == 0 {
				count = big.NewInt(1)
			} else {
				count = new(big.Int).Exp(bigBase, big.NewInt(int64(r)), nil)
			}
			if rem.Cmp(count) < 0 {
				if r == 0 {
					return "", nil
				}
				indices := make([]uint64, r)
				tmp := new(big.Int).Set(rem)
				mod := new(big.Int)
				for k := r - 1; k >= 0; k-- {
					tmp.DivMod(tmp, bigBase, mod)
					indices[k] = mod.Uint64()
				}
				var sb strings.Builder
				for _, i := range indices {
					sb.WriteString(lines[i])
				}
				return sb.String(), nil
			}
			rem.Sub(rem, count)
		}
		return "", fmt.Errorf("index out of range")
	}

	return "", fmt.Errorf("unsupported direct ItemAt on node kind %d", n.Kind)
}
