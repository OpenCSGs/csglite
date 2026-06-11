package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func TestParseBuiltInProviderHTML(t *testing.T) {
	cases := []struct {
		name  string
		parse func(*html.Node) []Result
		body  string
		want  Result
	}{
		{
			name:  ProviderBing,
			parse: parseBingResults,
			body:  `<ol><li class="b_algo"><h2><a href="https://example.com/bing"> Bing Title </a></h2><p>Bing snippet</p></li></ol>`,
			want:  Result{Title: "Bing Title", URL: "https://example.com/bing", Snippet: "Bing snippet", Engine: ProviderBing},
		},
		{
			name:  ProviderBaidu,
			parse: parseBaiduResults,
			body:  `<div class="result"><h3><a href="https://example.com/baidu">百度标题</a></h3><div class="c-abstract">百度摘要</div></div>`,
			want:  Result{Title: "百度标题", URL: "https://example.com/baidu", Snippet: "百度摘要", Engine: ProviderBaidu},
		},
		{
			name:  ProviderDuckDuckGo,
			parse: parseDuckDuckGoResults,
			body:  `<div class="result"><a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fddg">DDG Title</a><a class="result__snippet">DDG snippet</a></div>`,
			want:  Result{Title: "DDG Title", URL: "https://example.com/ddg", Snippet: "DDG snippet", Engine: ProviderDuckDuckGo},
		},
		{
			name:  ProviderSogou,
			parse: parseSogouResults,
			body:  `<div class="vrwrap"><h3 class="vr-title"><a href="/link?url=token">搜狗标题</a></h3><div class="ft">搜狗摘要</div><div data-url="https://example.com/sogou"></div></div>`,
			want:  Result{Title: "搜狗标题", URL: "https://example.com/sogou", Snippet: "搜狗摘要", Engine: ProviderSogou},
		},
		{
			name:  ProviderQuark,
			parse: parseQuarkResults,
			body:  `<script type="application/json" id="s-data-1" data-used-by="hydrate">{"data":{"initialData":{"title":"<em>夸克</em>标题","desc":"夸克摘要","url":"https://example.com/quark"}},"extraData":{"sc":"nature_result"}}</script>`,
			want:  Result{Title: "夸克 标题", URL: "https://example.com/quark", Snippet: "夸克摘要", Engine: ProviderQuark},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			results := normalizeResults(tc.parse(doc), 5)
			if len(results) != 1 {
				t.Fatalf("len(results) = %d, want 1: %#v", len(results), results)
			}
			got := results[0]
			if got.Title != tc.want.Title || got.URL != tc.want.URL || got.Snippet != tc.want.Snippet || got.Engine != tc.want.Engine {
				t.Fatalf("result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSearchFallsBackAcrossProviders(t *testing.T) {
	var hits []string
	var hitsMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsMu.Lock()
		hits = append(hits, r.URL.Path)
		hitsMu.Unlock()
		switch r.URL.Path {
		case "/bing":
			http.Error(w, "blocked", http.StatusTooManyRequests)
		case "/ddg":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<div class="result"><a class="result__a" href="https://example.com/fallback">Lite search fallback</a><a class="result__snippet">Lite search fallback snippet</a></div>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resp, err := Search(context.Background(), Config{
		Providers: []string{ProviderBing, ProviderDuckDuckGo},
		ProviderBaseURLs: map[string]string{
			ProviderBing:       srv.URL + "/bing",
			ProviderDuckDuckGo: srv.URL + "/ddg",
		},
	}, SearchRequest{Query: "lite search"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if resp.Provider != ProviderDuckDuckGo {
		t.Fatalf("provider = %q, want %q", resp.Provider, ProviderDuckDuckGo)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://example.com/fallback" {
		t.Fatalf("results = %#v", resp.Results)
	}
	hitsMu.Lock()
	gotHits := strings.Join(hits, ",")
	hitsMu.Unlock()
	if !strings.Contains(gotHits, "/bing") || !strings.Contains(gotHits, "/ddg") {
		t.Fatalf("hits = %q, want bing and ddg", gotHits)
	}
}

func TestParseSogouWeatherCardSnippet(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(`
		<div class="vrwrap">
			<h3 class="vr-title"><a href="https://weatherol.cn/index.html?cityid1=440300">深圳天气预报_一周天气预报</a></h3>
			<div class="desc-box">
				<div class="w-desc" style="display:none;"><div class="temperature">24~29<i>℃</i></div><p class="w-info"><span>大雨转中雨</span><span>东南风微风</span></p></div>
				<div class="w-desc currentDay"><div class="temperature">23~27<i>℃</i></div><p class="w-info"><span>暴雨转大雨</span><span>东风微风</span></p></div>
			</div>
		</div>`))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	results := normalizeResults(parseSogouResults(doc), 5)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1: %#v", len(results), results)
	}
	if !strings.Contains(results[0].Snippet, "23~27") || !strings.Contains(results[0].Snippet, "暴雨转大雨") {
		t.Fatalf("weather snippet = %q, want current weather details", results[0].Snippet)
	}
}

func TestSearchAggregatesAndInterleavesProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/baidu":
			_, _ = w.Write([]byte(`
				<div class="result"><h3><a href="https://example.com/baidu-1">湖人 2026 Baidu One</a></h3><div class="c-abstract">湖人 2026 snippet one</div></div>
				<div class="result"><h3><a href="https://example.com/baidu-2">湖人 2026 Baidu Two</a></h3><div class="c-abstract">湖人 2026 snippet two</div></div>
			`))
		case "/bing":
			_, _ = w.Write([]byte(`
				<ol>
					<li class="b_algo"><h2><a href="https://example.com/bing-1">湖人 2026 Bing One</a></h2><p>湖人 2026 snippet one</p></li>
					<li class="b_algo"><h2><a href="https://example.com/bing-2">湖人 2026 Bing Two</a></h2><p>湖人 2026 snippet two</p></li>
				</ol>
			`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resp, err := Search(context.Background(), Config{
		MaxResults: 3,
		Providers:  []string{ProviderBaidu, ProviderBing},
		ProviderBaseURLs: map[string]string{
			ProviderBaidu: srv.URL + "/baidu",
			ProviderBing:  srv.URL + "/bing",
		},
	}, SearchRequest{Query: "湖人 2026"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if resp.Provider != "baidu,bing" {
		t.Fatalf("provider = %q, want baidu,bing", resp.Provider)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len(results) = %d, want 3: %#v", len(resp.Results), resp.Results)
	}
	wantURLs := []string{"https://example.com/baidu-1", "https://example.com/bing-1", "https://example.com/baidu-2"}
	for i, want := range wantURLs {
		if resp.Results[i].URL != want {
			t.Fatalf("result[%d].URL = %q, want %q; results=%#v", i, resp.Results[i].URL, want, resp.Results)
		}
	}
}

func TestQuickSearchReturnsBeforeSlowProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/bing":
			_, _ = w.Write([]byte(`
				<ol>
					<li class="b_algo"><h2><a href="https://example.com/lite-1">Lite search one</a></h2><p>Lite search snippet one</p></li>
					<li class="b_algo"><h2><a href="https://example.com/lite-2">Lite search two</a></h2><p>Lite search snippet two</p></li>
				</ol>
			`))
		case "/ddg":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := Search(context.Background(), Config{
		MaxResults: 2,
		Quick:      true,
		Providers:  []string{ProviderBing, ProviderDuckDuckGo},
		ProviderBaseURLs: map[string]string{
			ProviderBing:       srv.URL + "/bing",
			ProviderDuckDuckGo: srv.URL + "/ddg",
		},
		Timeout: time.Second,
	}, SearchRequest{Query: "lite search"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Search() took %s, want quick return before slow provider", elapsed)
	}
	if resp.Provider != ProviderBing {
		t.Fatalf("provider = %q, want %q", resp.Provider, ProviderBing)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2: %#v", len(resp.Results), resp.Results)
	}
}

func TestQuickSearchHonorsProviderOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/bing":
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(`<ol><li class="b_algo"><h2><a href="https://example.com/bing">Lite search from Bing</a></h2><p>Lite search snippet.</p></li></ol>`))
		case "/ddg":
			_, _ = w.Write([]byte(`<div class="result"><a class="result__a" href="https://example.com/ddg">Lite search from DDG</a><a class="result__snippet">Lite search snippet.</a></div>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resp, err := Search(context.Background(), Config{
		MaxResults: 1,
		Quick:      true,
		Providers:  []string{ProviderBing, ProviderDuckDuckGo},
		ProviderBaseURLs: map[string]string{
			ProviderBing:       srv.URL + "/bing",
			ProviderDuckDuckGo: srv.URL + "/ddg",
		},
		Timeout: time.Second,
	}, SearchRequest{Query: "lite search"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if resp.Provider != ProviderBing {
		t.Fatalf("provider = %q, want %q", resp.Provider, ProviderBing)
	}
}

func TestSearchFiltersIrrelevantResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
			<ol>
				<li class="b_algo"><h2><a href="https://example.com/input-method">万能五笔输入法 2026官方版下载</a></h2><p>输入法下载。</p></li>
				<li class="b_algo"><h2><a href="https://example.com/lakers">湖人季后赛战绩更新</a></h2><p>湖人本赛季季后赛战绩为1胜4负。</p></li>
			</ol>
		`))
	}))
	defer srv.Close()

	resp, err := Search(context.Background(), Config{
		Providers: []string{ProviderBing},
		ProviderBaseURLs: map[string]string{
			ProviderBing: srv.URL + "/search",
		},
	}, SearchRequest{Query: "今年季后赛湖人战绩如何 2026"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("len(results) = %d, want 1: %#v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].URL != "https://example.com/lakers" {
		t.Fatalf("result URL = %q, want lakers", resp.Results[0].URL)
	}
}

func TestSearchFiltersAccidentalChineseBigramMatch(t *testing.T) {
	results := filterResultsByQueryRelevance("今年季后赛湖人战绩如何 2026", []Result{
		{
			Title:   "今年会出现超强厄尔尼诺吗",
			URL:     "https://example.com/weather",
			Snippet: "今年春季后期可能进入厄尔尼诺状态。",
			Engine:  ProviderBing,
		},
		{
			Title:   "湖人季后赛战绩更新",
			URL:     "https://example.com/lakers",
			Snippet: "湖人本赛季季后赛战绩为1胜4负。",
			Engine:  ProviderBing,
		},
	})
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1: %#v", len(results), results)
	}
	if results[0].URL != "https://example.com/lakers" {
		t.Fatalf("result URL = %q, want lakers", results[0].URL)
	}
}

func TestSearchHonorsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	_, err := Search(context.Background(), Config{
		Providers: []string{ProviderBing},
		ProviderBaseURLs: map[string]string{
			ProviderBing: srv.URL + "/search",
		},
		Timeout: 10 * time.Millisecond,
	}, SearchRequest{Query: "lite"})
	if err == nil {
		t.Fatal("Search() error = nil, want timeout")
	}
}

func TestDefaultProviderOrderPrefersSogouForChinese(t *testing.T) {
	got := defaultProviderOrder("zh-CN")
	if len(got) == 0 || got[0] != ProviderSogou {
		t.Fatalf("defaultProviderOrder(zh-CN) = %#v", got)
	}
	if strings.Join(got, ",") != "sogou,baidu,quark,bing,duckduckgo" {
		t.Fatalf("defaultProviderOrder(zh-CN) = %#v, want Sogou/Baidu/Quark/Bing/DuckDuckGo", got)
	}
}
