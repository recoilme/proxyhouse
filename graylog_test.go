package main

import (
	"bytes"
	"compress/zlib"
	"io"
	"testing"
)

func TestGraylog(t *testing.T) {
	gl := NewGraylog(Graylog{
		LogLevel:  LEVEL_DBG,
		ChunkSize: 2048,
		Hostname:  "testhostname",
		Filename:  "screwdriver",
	})
	message := "To write a new test suite, create a file whose name ends _test.go"
	msg := gl.Message(LEVEL_INFO, message)
	if msg.Version != "1.1" {
		t.Errorf("Version: want '1.1'; got '%s'", msg.Version)
	}
	if msg.Host != "testhostname" {
		t.Errorf("Host: want 'testhostname'; got '%s'", msg.Host)
	}
	if msg.Short != message {
		t.Errorf("Short: want\n%s\ngot\n%s", message, msg.Short)
	}
	if msg.Full != message {
		t.Errorf("Full: want\n%s\ngot\n%s", message, msg.Full)
	}

	long_message := "To write a new test suite, create a file whose name ends _test.go that contains the TestXxx functions as described here. Put the file in the same package as the one being tested. The file will be excluded from regular package builds but will be included when the “go test” command is run."
	msg = gl.Message(LEVEL_INFO, long_message)
	short_message := long_message[:125] + "..."
	if msg.Short != short_message {
		t.Errorf("Short2: want\n%s\ngot\n%s", short_message, msg.Short)
	}
	if msg.Full != long_message {
		t.Errorf("Full2: want\n%s\ngot\n%s", long_message, msg.Full)
	}

	msg.Timestamp = 1594916275
	compressed, err := gl.PackMessage(msg)
	if err != nil {
		panic(err)
	}

	b := bytes.NewReader(compressed)
	r, err := zlib.NewReader(b)
	if err != nil {
		panic(err)
	}
	data := make([]byte, 2048)
	n, err := r.Read(data)
	if err != nil && err != io.EOF {
		panic(err)
	}
	r.Close()

	want := "{\"version\":\"1.1\",\"host\":\"testhostname\",\"timestamp\":1594916275,\"file\":\"screwdriver\",\"level\":6,\"short_message\":\"" + short_message + "\",\"full_message\":\"" + long_message + "\"}"
	if n != len(want) {
		t.Errorf("JSON length: want %d; got %d", len(want), n)
	}

	got := string(data[:n])
	if want != got {
		t.Errorf("JSON: want '%s'; got '%s'", want, got)
	}

	gl.Info(long_message)
}
