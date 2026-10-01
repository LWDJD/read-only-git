package arweave

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// checkGateway 起一个假网关：/info 供预检探活，/raw/<id> 给内容，没给的 404。
func checkGateway(files map[string][]byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"height":1,"queue_length":0,"network":"arweave-N.1"}`))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/raw/")
		if data, ok := files[id]; ok {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
}

func testRecord() *publish.Record {
	return &publish.Record{
		Root:  "root-id",
		Files: map[string]string{"a.txt": publish.Digest([]byte("aaa")), "b.txt": publish.Digest([]byte("bbb"))},
		Refs:  map[string]string{"a.txt": "id-a", "b.txt": "id-b"},
	}
}

const testManifest = `{"manifest":"arweave/paths","version":"0.2.0","paths":{"a.txt":{"id":"id-a"},"b.txt":{"id":"id-b"}}}`

// 一个网关读到就算成功：网关差异只作备注，不把成功标成可疑。
func TestCheckSiteOneGatewayIsEnough(t *testing.T) {
	gw1 := checkGateway(map[string][]byte{
		"root-id": []byte(testManifest),
		"id-a":    []byte("aaa"),
		"id-b":    []byte("bbb"),
	})
	defer gw1.Close()
	gw2 := checkGateway(map[string][]byte{
		"root-id": []byte(testManifest),
		"id-a":    []byte("aaa"),
	})
	defer gw2.Close()

	rep, err := CheckSite(context.Background(), CheckOptions{
		Record:   testRecord(),
		Gateways: []string{gw1.URL, gw2.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.EntryOK {
		t.Fatalf("入口应当可读: %s", rep.EntryMsg)
	}
	for _, it := range rep.Items {
		if it.Verdict != VerdictOK {
			t.Fatalf("%s 有一个网关读到就该 ok，实际 %s", it.Path, it.Verdict)
		}
	}
	// b.txt 的网关差异应当记在 detail 里（不影响结论）
	for _, it := range rep.Items {
		if it.Path == "b.txt" && it.Detail == "" {
			t.Fatal("网关差异应当记进 detail")
		}
	}
	if rep.GatewayDiff == 0 {
		t.Fatal("b.txt 有网关差异，GatewayDiff 应当计到")
	}
}

// 所有网关都读不到才算 missing。
func TestCheckSiteMissingWhenAllGatewaysFail(t *testing.T) {
	gw := checkGateway(map[string][]byte{"root-id": []byte(testManifest)})
	defer gw.Close()

	rep, err := CheckSite(context.Background(), CheckOptions{
		Record:   testRecord(),
		Gateways: []string{gw.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range rep.Items {
		if it.Verdict != VerdictMissing {
			t.Fatalf("%s 所有网关都读不到，应当 missing，实际 %s", it.Path, it.Verdict)
		}
	}
	if rep.Missing != 2 {
		t.Fatalf("missing 计数应为 2，实际 %d", rep.Missing)
	}
}

// 内容摘要核对能揪出「读得到但内容不对」。
func TestCheckContentFlagsMismatch(t *testing.T) {
	gw := checkGateway(map[string][]byte{
		"root-id": []byte(testManifest),
		"id-a":    []byte("被换过的内容"),
		"id-b":    []byte("bbb"),
	})
	defer gw.Close()

	rep, err := CheckSite(context.Background(), CheckOptions{
		Record:       testRecord(),
		Gateways:     []string{gw.URL},
		CheckContent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Verdict{}
	for _, it := range rep.Items {
		byPath[it.Path] = it.Verdict
	}
	if byPath["a.txt"] != VerdictMismatch {
		t.Fatalf("a.txt 内容被换过，应当 mismatch，实际 %s", byPath["a.txt"])
	}
	if byPath["b.txt"] != VerdictOK {
		t.Fatalf("b.txt 内容一致，应当 ok，实际 %s", byPath["b.txt"])
	}
}

// repair 只清坏引用，好引用一条不动；入口失效时连 Root 一起清。
func TestRepairClearsOnlyBadRefs(t *testing.T) {
	rec := testRecord()
	rep := &CheckReport{Items: []ItemCheck{
		{Path: "a.txt", Verdict: VerdictOK},
		{Path: "b.txt", Verdict: VerdictMissing},
	}}

	n := RepairRecord(rec, rep)
	if n != 1 {
		t.Fatalf("应清 1 条，实际 %d", n)
	}
	if _, ok := rec.Refs["a.txt"]; !ok {
		t.Fatal("好引用不该被清")
	}
	if _, ok := rec.Refs["b.txt"]; ok {
		t.Fatal("坏引用应当被清")
	}

	// mismatch 同样清
	rec2 := testRecord()
	rep2 := &CheckReport{Items: []ItemCheck{{Path: "a.txt", Verdict: VerdictMismatch}}}
	if n := RepairRecord(rec2, rep2); n != 1 {
		t.Fatalf("mismatch 也该清，实际清了 %d", n)
	}

	// unreachable 不清：那是网络问题，清了会让已付费的内容重传
	rec3 := testRecord()
	rep3 := &CheckReport{Items: []ItemCheck{{Path: "a.txt", Verdict: VerdictUnreachable}}}
	if n := RepairRecord(rec3, rep3); n != 0 {
		t.Fatalf("unreachable 不该清，实际清了 %d", n)
	}
}
