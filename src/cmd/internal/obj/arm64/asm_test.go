// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package arm64

import (
	"bytes"
	"fmt"
	"internal/testenv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func runAssembler(t *testing.T, srcdata string) []byte {
	dir := t.TempDir()
	defer os.RemoveAll(dir)
	srcfile := filepath.Join(dir, "testdata.s")
	outfile := filepath.Join(dir, "testdata.o")
	os.WriteFile(srcfile, []byte(srcdata), 0644)
	cmd := testenv.Command(t, testenv.GoToolPath(t), "tool", "asm", "-S", "-o", outfile, srcfile)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("The build failed: %v, output:\n%s", err, out)
	}
	return out
}

func TestSplitImm24uScaled(t *testing.T) {
	tests := []struct {
		v       int32
		shift   int
		wantErr bool
		wantHi  int32
		wantLo  int32
	}{
		{
			v:      0,
			shift:  0,
			wantHi: 0,
			wantLo: 0,
		},
		{
			v:      0x1001,
			shift:  0,
			wantHi: 0x1000,
			wantLo: 0x1,
		},
		{
			v:      0xffffff,
			shift:  0,
			wantHi: 0xfff000,
			wantLo: 0xfff,
		},
		{
			v:       0xffffff,
			shift:   1,
			wantErr: true,
		},
		{
			v:      0xfe,
			shift:  1,
			wantHi: 0x0,
			wantLo: 0x7f,
		},
		{
			v:      0x10fe,
			shift:  1,
			wantHi: 0x0,
			wantLo: 0x87f,
		},
		{
			v:      0x2002,
			shift:  1,
			wantHi: 0x2000,
			wantLo: 0x1,
		},
		{
			v:      0xfffffe,
			shift:  1,
			wantHi: 0xffe000,
			wantLo: 0xfff,
		},
		{
			v:      0x1000ffe,
			shift:  1,
			wantHi: 0xfff000,
			wantLo: 0xfff,
		},
		{
			v:       0x1001000,
			shift:   1,
			wantErr: true,
		},
		{
			v:       0xfffffe,
			shift:   2,
			wantErr: true,
		},
		{
			v:      0x4004,
			shift:  2,
			wantHi: 0x4000,
			wantLo: 0x1,
		},
		{
			v:      0xfffffc,
			shift:  2,
			wantHi: 0xffc000,
			wantLo: 0xfff,
		},
		{
			v:      0x1002ffc,
			shift:  2,
			wantHi: 0xfff000,
			wantLo: 0xfff,
		},
		{
			v:       0x1003000,
			shift:   2,
			wantErr: true,
		},
		{
			v:       0xfffffe,
			shift:   3,
			wantErr: true,
		},
		{
			v:      0x8008,
			shift:  3,
			wantHi: 0x8000,
			wantLo: 0x1,
		},
		{
			v:      0xfffff8,
			shift:  3,
			wantHi: 0xff8000,
			wantLo: 0xfff,
		},
		{
			v:      0x1006ff8,
			shift:  3,
			wantHi: 0xfff000,
			wantLo: 0xfff,
		},
		{
			v:       0x1007000,
			shift:   3,
			wantErr: true,
		},
	}
	for _, test := range tests {
		hi, lo, err := splitImm24uScaled(test.v, test.shift)
		switch {
		case err == nil && test.wantErr:
			t.Errorf("splitImm24uScaled(%v, %v) succeeded, want error", test.v, test.shift)
		case err != nil && !test.wantErr:
			t.Errorf("splitImm24uScaled(%v, %v) failed: %v", test.v, test.shift, err)
		case !test.wantErr:
			if got, want := hi, test.wantHi; got != want {
				t.Errorf("splitImm24uScaled(%x, %x) - got hi %x, want %x", test.v, test.shift, got, want)
			}
			if got, want := lo, test.wantLo; got != want {
				t.Errorf("splitImm24uScaled(%x, %x) - got lo %x, want %x", test.v, test.shift, got, want)
			}
		}
	}
	for shift := 0; shift <= 3; shift++ {
		for v := int32(0); v < 0xfff000+0xfff<<shift; v = v + 1<<shift {
			hi, lo, err := splitImm24uScaled(v, shift)
			if err != nil {
				t.Fatalf("splitImm24uScaled(%x, %x) failed: %v", v, shift, err)
			}
			if hi+lo<<shift != v {
				t.Fatalf("splitImm24uScaled(%x, %x) = (%x, %x) is incorrect", v, shift, hi, lo)
			}
		}
	}
}

// TestLarge generates a very large file to verify that large
// program builds successfully, in particular, too-far
// conditional branches are fixed, and also verify that the
// instruction's pc can be correctly aligned even when branches
// need to be fixed.
func TestLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip in short mode")
	}
	testenv.MustHaveGoBuild(t)

	// generate a very large function
	buf := bytes.NewBuffer(make([]byte, 0, 7000000))
	fmt.Fprintln(buf, "TEXT f(SB),0,$0-0")
	fmt.Fprintln(buf, "TBZ $5, R0, label")
	fmt.Fprintln(buf, "CBZ R0, label")
	fmt.Fprintln(buf, "BEQ label")
	fmt.Fprintln(buf, "PCALIGN $128")
	fmt.Fprintln(buf, "MOVD $3, R3")
	for i := 0; i < 1<<19; i++ {
		fmt.Fprintln(buf, "MOVD R0, R1")
	}
	fmt.Fprintln(buf, "label:")
	fmt.Fprintln(buf, "RET")

	// assemble generated file
	out := runAssembler(t, buf.String())

	pattern := `0x0080\s00128\s\(.*\)\tMOVD\t\$3,\sR3`
	matched, err := regexp.MatchString(pattern, string(out))

	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Errorf("The alignment is not correct: %t\n", matched)
	}
}

// Issue 20348.
func TestNoRet(t *testing.T) {
	runAssembler(t, "TEXT ·stub(SB),$0-0\nNOP\n")
}

// TestPCALIGN verifies the correctness of the PCALIGN by checking if the
// code can be aligned to the alignment value.
func TestPCALIGN(t *testing.T) {
	testenv.MustHaveGoBuild(t)

	code1 := "TEXT ·foo(SB),$0-0\nMOVD $0, R0\nPCALIGN $8\nMOVD $1, R1\nRET\n"
	code2 := "TEXT ·foo(SB),$0-0\nMOVD $0, R0\nPCALIGN $16\nMOVD $2, R2\nRET\n"
	// If the output contains this pattern, the pc-offset of "MOVD $1, R1" is 8 bytes aligned.
	out1 := `0x0008\s00008\s\(.*\)\tMOVD\t\$1,\sR1`
	// If the output contains this pattern, the pc-offset of "MOVD $2, R2" is 16 bytes aligned.
	out2 := `0x0010\s00016\s\(.*\)\tMOVD\t\$2,\sR2`
	var testCases = []struct {
		name string
		code string
		out  string
	}{
		{"8-byte alignment", code1, out1},
		{"16-byte alignment", code2, out2},
	}

	for _, test := range testCases {
		out := runAssembler(t, test.code)
		matched, err := regexp.MatchString(test.out, string(out))
		if err != nil {
			t.Fatal(err)
		}
		if !matched {
			t.Errorf("The %s testing failed!\ninput: %s\noutput: %s\n", test.name, test.code, out)
		}
	}
}

// gen generates function with set size
func gen(buf *bytes.Buffer, name string, size int) {
	fmt.Fprintln(buf, "TEXT ", name, "(SB),0,$0-0")

	for i := 0; i < (size << 4); i++ {
		fmt.Fprintln(buf, "RET")
	}
}

// TestFarCondBr19 makes sure that tramline insertion works when branch target further than +-1Mb
func TestFarCondBr19(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip in short mode")
	}
	testenv.MustHaveGoBuild(t)

	dir, err := os.MkdirTemp("", "testcondbranch19")
	if err != nil {
		t.Fatalf("could not create directory: %v", err)
	}
	defer os.RemoveAll(dir)

	const branchDistance = 1 << (19 + 1)
	const dummyFuncSize = branchDistance / 2

	// generate few a very large function
	buf := bytes.NewBuffer(make([]byte, 0, 2*branchDistance*4+1024))

	for i := 0; i*dummyFuncSize < branchDistance; i++ {
		gen(buf, "·topdummyfunction"+strconv.Itoa(i), dummyFuncSize)
	}

	fmt.Fprintln(buf, "TEXT ·fartarget(SB),0,$0-0")
	fmt.Fprintln(buf, "MOVD $42, R0")
	fmt.Fprintln(buf, "B ·bottomdummyfunction0(SB)")
	fmt.Fprintln(buf, "B ·bottomdummyfunction1(SB)")
	fmt.Fprintln(buf, "B ·topdummyfunction0(SB)")
	fmt.Fprintln(buf, "B ·topdummyfunction1(SB)")
	fmt.Fprintln(buf, "RET")

	for i := 0; i*dummyFuncSize < branchDistance; i++ {
		gen(buf, "·bottomdummyfunction"+strconv.Itoa(i), dummyFuncSize)
	}

	tmpfile1 := filepath.Join(dir, "fartarget_arm64.s")
	err = os.WriteFile(tmpfile1, buf.Bytes(), 0644)
	if err != nil {
		t.Fatalf("can't write output: %v\n", err)
	}

	// generate function with CBZ
	buf.Reset()

	fmt.Fprintln(buf, "TEXT ·farcondbr19(SB),0,$0-8")
	fmt.Fprintln(buf, "MOVD $0, R0")
	fmt.Fprintln(buf, "CBZ R0, ·fartarget(SB)")
	fmt.Fprintln(buf, "MOVD R0, ret+0(FP)")
	fmt.Fprintln(buf, "RET")

	tmpfile2 := filepath.Join(dir, "condbr19_arm64.s")
	err = os.WriteFile(tmpfile2, buf.Bytes(), 0644)
	if err != nil {
		t.Fatalf("can't write output: %v\n", err)
	}

	// generate function with CBZ
	buf.Reset()

	fmt.Fprintln(buf, "package main")
	fmt.Fprintln(buf, "import \"fmt\"")
	fmt.Fprintln(buf, "func farcondbr19() uint64")
	fmt.Fprintln(buf, "func main() { fmt.Print(farcondbr19()) }")

	tmpfile3 := filepath.Join(dir, "main.go")
	err = os.WriteFile(tmpfile3, buf.Bytes(), 0644)
	if err != nil {
		t.Fatalf("can't write output: %v\n", err)
	}

	// generate go.mod
	buf.Reset()

	fmt.Fprintln(buf, "module testcondbr19")
	fmt.Fprintln(buf, "go 1.23") // TODO fix this

	tmpfile4 := filepath.Join(dir, "go.mod")
	err = os.WriteFile(tmpfile4, buf.Bytes(), 0644)
	if err != nil {
		t.Fatalf("can't write output: %v\n", err)
	}

	// build test
	fmt.Println("Build")
	cmd := testenv.Command(t, testenv.GoToolPath(t), "build", "-C", dir, "-o", "testcondbr19.out")
	fmt.Println(cmd)
	cmd.Env = append(os.Environ(), "GOOS=linux")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("build failed: %v, output: %s", err, out)
	}

	cmd = testenv.Command(t, filepath.Join(dir, "testcondbr19.out"))
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Errorf("runnig test failed: %v, output: %s", err, out)
	}

	if !(len(out) == 2 && out[0] == '4' && out[1] == '2') {
		t.Errorf("test returned: %s wanted: %s", out, "42")
	}
}
