package main

import (
	"fmt"
	"github.com/marpaia/graphite-golang"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkReq(b *testing.B) {
	metricStorage = NewMetricStorage()
	*graphitehost = ""
	gr = graphite.NewGraphiteNop(*graphitehost, *graphiteport)
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(dorequest)
	cnt := 0
	table := "t"
	for i := 0; i < b.N; i++ {
		cnt++
		if cnt == 1000 {
			s := i%3
			table = "t_" + strconv.Itoa(s)
			cnt = 0
		}
		bodyReader := strings.NewReader(fmt.Sprint(i+i))
		uri := "/?query=INSERT%20INTO%20"+table+"%20VALUES"
		req, err := http.NewRequest("POST", uri, bodyReader)
		if err != nil {
			b.Fatal(err)
		}
		handler.ServeHTTP(rr, req)
		metricStorage.storage = make(map[string]int)
	}
}
