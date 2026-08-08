package service

import (
	"net/http"
	"sync"
	"testing"
)

func TestGetHttpClientWithProxyNormalizesAndCachesLegacyURL(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	first, err := GetHttpClientWithProxy("HTTP://proxy.example:8080/legacy?unused=true")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy: %v", err)
	}
	second, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy canonical URL: %v", err)
	}
	if first != second {
		t.Fatal("legacy and canonical proxy URLs did not share the cached client")
	}
}

func TestGetHttpClientWithProxyCreatesOneClientConcurrently(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	const workers = 32
	clients := make([]*http.Client, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer group.Done()
			client, err := GetHttpClientWithProxy("socks5://proxy.example")
			clients[index] = client
			errs[index] = err
		}(i)
	}
	group.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
	}
	first := clients[0]
	for index := 1; index < workers; index++ {
		if clients[index] != first {
			t.Fatalf("worker %d received a different cached client", index)
		}
	}
}

func TestInvalidateProxyClientRemovesCanonicalClient(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	first, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy: %v", err)
	}
	InvalidateProxyClient("HTTP://proxy.example:8080/legacy")
	second, err := GetHttpClientWithProxy("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("GetHttpClientWithProxy after invalidation: %v", err)
	}
	if first == second {
		t.Fatal("proxy client was not invalidated")
	}
}
