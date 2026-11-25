package gomap

import (
	"context"
	"math/big"
	"testing"
)

func BenchmarkFixedOdometer_Lower4(b *testing.B) {
	eng := NewEngine()
	plan, err := eng.Compile("/a{4}", nil)
	if err != nil {
		b.Fatal(err)
	}
	st, err := eng.Stats(plan, 1, "", "", false, false)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(st.TotalBytes.Int64())
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := eng.GenerateRange(context.Background(), plan, big.NewInt(0), st.TotalWords.Uint64(), "\n", 512*1024, func(_ []byte, _ uint64) error {
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGeneralWalker_Unions(b *testing.B) {
	eng := NewEngine()
	plan, err := eng.Compile("[admin|root|guest|operator]_[dev|prod|stage]_#[000-999]", nil)
	if err != nil {
		b.Fatal(err)
	}
	st, err := eng.Stats(plan, 1, "", "", false, false)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(st.TotalBytes.Int64())
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := eng.GenerateRange(context.Background(), plan, big.NewInt(0), st.TotalWords.Uint64(), "\n", 512*1024, func(_ []byte, _ uint64) error {
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSmartSeek_BillionRange(b *testing.B) {
	eng := NewEngine()
	plan, err := eng.Compile("host_#[0-4294967295]", nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := eng.Stats(plan, 1, "host_4294967200", "host_4294967295", true, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}
