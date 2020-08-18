package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort      = 12201
	defaultHost      = "127.0.0.1"
	defaultChunkSize = 8192
	LEVEL_ALERT      = 1
	LEVEL_CRIT       = 2
	LEVEL_ERR        = 3
	LEVEL_WARN       = 4
	LEVEL_NOTICE     = 5
	LEVEL_INFO       = 6
	LEVEL_DBG        = 7
)

type Graylog struct {
	Host      string
	Port      int
	ChunkSize int
	Hostname  string
	Filename  string
	Connect   *net.UDPConn
	MessageID uint64
	LogLevel  uint8
}

type GLMessage struct {
	Version   string `json:"version"`
	Host      string `json:"host"`
	Timestamp int64  `json:"timestamp"`
	File      string `json:"file"`
	Level     uint8  `json:"level"`
	Short     string `json:"short_message"`
	Full      string `json:"full_message"`
}

var logLevels = map[string]uint8{
	"debug":    LEVEL_DBG,
	"info":     LEVEL_INFO,
	"notice":   LEVEL_NOTICE,
	"warn":     LEVEL_WARN,
	"error":    LEVEL_ERR,
	"critical": LEVEL_CRIT,
	"alert":    LEVEL_ALERT,
}

func NewGraylog(opts Graylog) *Graylog {
	if opts.Host == "" {
		opts.Host = defaultHost
	}
	if opts.Port == 0 {
		opts.Port = defaultPort
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = defaultChunkSize
	}
	if opts.Hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		opts.Hostname = hostname
	}
	if opts.Filename == "" {
		filename := filepath.Base(os.Args[0])
		opts.Filename = filename
	}
	if opts.LogLevel == 0 {
		opts.LogLevel = LEVEL_INFO
	}
	return &opts
}

func Connect(host string, port int) (*net.UDPConn, error) {
	var addr = host + ":" + strconv.Itoa(port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (gl *Graylog) Compress(b []byte) bytes.Buffer {
	var buf bytes.Buffer
	comp := zlib.NewWriter(&buf)
	comp.Write(b)
	comp.Close()
	return buf
}

func (gl *Graylog) Send(b []byte) {
	if gl.Connect == nil {
		conn, err := Connect(gl.Host, gl.Port)
		if err != nil {
			return
		}
		gl.Connect = conn
	}
	gl.Connect.Write(b)
}

func (gl *Graylog) Message(level uint8, msg string) *GLMessage {
	message := &GLMessage{
		Version:   "1.1",
		Host:      gl.Hostname,
		Timestamp: time.Now().Unix(),
		Level:     level,
		File:      gl.Filename,
		Full:      msg,
	}
	if len(msg) < 128 {
		message.Short = msg
	} else {
		ind := strings.Index(msg, "\n")
		if ind < 0 || ind > 128 {
			message.Short = msg[:125] + "..."
		} else {
			message.Short = msg[:ind]
		}
	}
	return message
}

func (gl *Graylog) PackMessage(message *GLMessage) ([]byte, error) {
	jsondata, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	comp := zlib.NewWriter(&buf)
	comp.Write(jsondata)
	comp.Close()
	return buf.Bytes(), nil
}

func (gl *Graylog) Append(level uint8, data ...interface{}) {
	strbuf := bytes.Buffer{}
	for _, s := range data {
		strbuf.WriteString(fmt.Sprint(s, " "))
	}

	msg := gl.Message(level, strbuf.String())
	buf, err := gl.PackMessage(msg)
	if err != nil {
		return
	}
	length := len(buf)
	size := gl.ChunkSize
	if length < size {
		gl.Send(buf)
		return
	}
	count := int(math.Ceil(float64(length) / float64(size)))
	packet := make([]byte, size+12)
	copy(packet[0:2], []byte{0x1e, 0x0f})
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(gl.MessageID))
	gl.MessageID++
	copy(packet[2:10], b)
	packet[11] = byte(count)
	index := 0

	for offset := 0; offset < length; offset = offset + size {
		packet[10] = byte(index)
		if offset+size > length {
			copy(packet[12:], buf[offset:length])
		} else {
			copy(packet[12:], buf[offset:offset+size])
			gl.Send(packet)
		}
	}
}

func (gl *Graylog) Log(level uint8, data ...interface{}) {
	if level <= gl.LogLevel {
		gl.Append(level, data...)
	}
}

func (gl *Graylog) Debug(data ...interface{}) {
	gl.Log(LEVEL_DBG, data...)
}

func (gl *Graylog) Info(data ...interface{}) {
	gl.Log(LEVEL_INFO, data...)
}

func (gl *Graylog) Warn(data ...interface{}) {
	gl.Log(LEVEL_WARN, data...)
}

func (gl *Graylog) Error(data ...interface{}) {
	gl.Log(LEVEL_ERR, data...)
}

func (gl *Graylog) Critical(data ...interface{}) {
	gl.Log(LEVEL_CRIT, data)
}
