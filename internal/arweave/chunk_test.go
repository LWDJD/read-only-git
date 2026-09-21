package arweave

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// 单块：offset 指向末字节，切出来应当正好是整份数据。
func TestSplitChunksSingleChunk(t *testing.T) {
	data := []byte("hello")
	got, err := SplitChunks(data, []ChunkProof{{DataPath: "p", Offset: "4"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应当切成 1 块，实际 %d", len(got))
	}
	if got[0].Min != 0 || got[0].Max != 5 {
		t.Fatalf("区间应是 [0,5)，实际 [%d,%d)", got[0].Min, got[0].Max)
	}
}

// 用 arweave-js 实测的 offsets 验边界。
//
// 这几组数字直接取自真 arweave-js 的 generateTransactionChunks，
// 摆在这里是为了让「Go 侧的切法与它一致」这件事随时可查证，
// 而不是靠一句注释自说自话。
//
// 262145 那一组尤其值得留意：总长只比一块的上限多 1 字节，
// 于是被平分成 131073 + 131072，而不是 262144 + 1。
func TestSplitChunksMatchesArweaveJS(t *testing.T) {
	cases := []struct {
		size    int
		offsets []int
		ranges  [][2]int
	}{
		{1, []int{0}, [][2]int{{0, 1}}},
		{32768, []int{32767}, [][2]int{{0, 32768}}},
		{262144, []int{262143}, [][2]int{{0, 262144}}},
		{262145, []int{131072, 262144}, [][2]int{{0, 131073}, {131073, 262145}}},
		{300000, []int{262143, 299999}, [][2]int{{0, 262144}, {262144, 300000}}},
		{
			1048576,
			[]int{262143, 524287, 786431, 1048575},
			[][2]int{{0, 262144}, {262144, 524288}, {524288, 786432}, {786432, 1048576}},
		},
	}

	for _, c := range cases {
		data := make([]byte, c.size)
		for i := range data {
			data[i] = byte(i & 0xff)
		}
		proofs := make([]ChunkProof, len(c.offsets))
		for i, off := range c.offsets {
			proofs[i] = ChunkProof{DataPath: "p" + strconv.Itoa(i), Offset: strconv.Itoa(off)}
		}

		got, err := SplitChunks(data, proofs)
		if err != nil {
			t.Fatalf("%d 字节: %v", c.size, err)
		}
		if len(got) != len(c.ranges) {
			t.Fatalf("%d 字节: 应切 %d 块，实际 %d", c.size, len(c.ranges), len(got))
		}
		for i, want := range c.ranges {
			if got[i].Min != want[0] || got[i].Max != want[1] {
				t.Fatalf("%d 字节第 %d 块: 应为 [%d,%d)，实际 [%d,%d)",
					c.size, i, want[0], want[1], got[i].Min, got[i].Max)
			}
		}

		// 拼回去应当与原文一字不差，证明区间没有重叠也没有缺口
		var joined []byte
		for _, r := range got {
			joined = append(joined, data[r.Min:r.Max]...)
		}
		if len(joined) != len(data) {
			t.Fatalf("%d 字节: 拼回去长度是 %d", c.size, len(joined))
		}
		for i := range joined {
			if joined[i] != data[i] {
				t.Fatalf("%d 字节: 拼回去第 %d 字节不对", c.size, i)
			}
		}
	}
}

// proof 与数据对不上时必须早点说清楚，别把半截数据发出去。
func TestSplitChunksRejectsBadInput(t *testing.T) {
	data := []byte("0123456789")

	cases := []struct {
		name   string
		proofs []ChunkProof
		word   string
	}{
		{"没有 proof", nil, "没有 proof"},
		{"offset 不是数字", []ChunkProof{{Offset: "abc"}}, "不是数字"},
		{"越界", []ChunkProof{{Offset: "99"}}, "越界"},
		{"覆盖不全", []ChunkProof{{Offset: "3"}}, "对不上"},
		{"往回退", []ChunkProof{{Offset: "5"}, {Offset: "2"}}, "往回退"},
	}

	for _, c := range cases {
		_, err := SplitChunks(data, c.proofs)
		if err == nil {
			t.Fatalf("%s: 应当报错", c.name)
		}
		if !strings.Contains(err.Error(), c.word) {
			t.Fatalf("%s: 错误信息应含 %q，实际 %v", c.name, c.word, err)
		}
	}
}

// 块内容取自 data 的对应区间，不该是另拷一份。
// 这里只验区间对得上，因为 ChunkRange 有意不存内容本身。
func TestSplitChunksRangesSelectRightBytes(t *testing.T) {
	data := []byte("AAAABBBBCC")
	proofs := []ChunkProof{{Offset: "3"}, {Offset: "7"}, {Offset: "9"}}

	got, err := SplitChunks(data, proofs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AAAA", "BBBB", "CC"}
	for i, r := range got {
		if s := string(data[r.Min:r.Max]); s != want[i] {
			t.Fatalf("第 %d 块应是 %q，实际 %q", i, want[i], s)
		}
	}
}

// 切出来的块内容必须与 arweave-js 记的 dataHash 一致。
//
// 上一个测试只验边界，这里验内容：同一份数据两边算出同一个哈希，
// 才算真的「切法一致」。哈希值取自真 arweave-js 的 chunks[i].dataHash。
func TestSplitChunksContentMatchesArweaveJS(t *testing.T) {
	// 262144 字节那一块在三个用例里重复出现，单独提出来
	const hashOfOneFullChunk = "2312394bd99545d9de131c24efb781e765ac1aec243f2ed9347597a793a415e9"

	cases := []struct {
		size    int
		offsets []int
		hashes  []string
	}{
		{1, []int{0}, []string{
			"6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d",
		}},
		{32768, []int{32767}, []string{
			"e11360251d1173650cdcd20f111d8f1ca2e412f572e8b36a4dc067121c1799b8",
		}},
		{262144, []int{262143}, []string{hashOfOneFullChunk}},
		{262145, []int{131072, 262144}, []string{
			"59143c73fbc669c18bdeb36c7cfa13888c03a2d935b18d939c628692360c16ff",
			"3236dbd4b01931b8915bc8b5b3733a25637db827f976b074eb3ef66d461e4fe8",
		}},
		{300000, []int{262143, 299999}, []string{
			hashOfOneFullChunk,
			"0de34f4029de7db4f571ed9b4334801b3337d37872a85573d14167db5dcb5e71",
		}},
		{1048576, []int{262143, 524287, 786431, 1048575}, []string{
			hashOfOneFullChunk, hashOfOneFullChunk, hashOfOneFullChunk, hashOfOneFullChunk,
		}},
	}

	for _, c := range cases {
		data := make([]byte, c.size)
		for i := range data {
			data[i] = byte(i & 0xff)
		}
		proofs := make([]ChunkProof, len(c.offsets))
		for i, off := range c.offsets {
			proofs[i] = ChunkProof{Offset: strconv.Itoa(off)}
		}

		got, err := SplitChunks(data, proofs)
		if err != nil {
			t.Fatalf("%d 字节: %v", c.size, err)
		}
		for i, r := range got {
			sum := sha256.Sum256(data[r.Min:r.Max])
			if hex.EncodeToString(sum[:]) != c.hashes[i] {
				t.Fatalf("%d 字节第 %d 块: 内容哈希与 arweave-js 不一致\n  想要 %s\n  实际 %s",
					c.size, i, c.hashes[i], hex.EncodeToString(sum[:]))
			}
		}
	}
}
