package main

import (
	"bytes"
	"testing"
)

func Test_Http(t *testing.T) {
	req := `POST /cgi-bin/process.cgi HTTP/1.1
User-Agent: Mozilla/4.0 (compatible; MSIE5.01; Windows NT)
Host: www.tutorialspoint.com
Content-Type: application/x-www-form-urlencoded
Content-Length: 3
Accept-Language: en-us
Accept-Encoding: gzip, deflate
Connection: Keep-Alive

lic`
	bin := bytes.ReplaceAll([]byte(req), []byte("\n"), []byte("\r\n"))
	//fmt.Printf("%+v\n", bin)
	lo, resp := httpproto(bin)
	if len(lo) != 0 {

		t.Errorf("must be zero:%+v\n", lo)
	}
	if !bytes.Equal(bin, resp) {
		t.Errorf("resp not eq:%+v\n", "")
	}
}
