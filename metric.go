package main

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

type MetricStorage struct {
	mx      sync.Mutex
	storage map[string]int
}

func NewMetricStorage() *MetricStorage {
	return &MetricStorage{
		storage: make(map[string]int),
	}
}

func (ms *MetricStorage) SendMetrics() {
	go func() {
		for {
			ms.mx.Lock()
			if len(ms.storage) != 0 {
				var bytesSent, sendDuration int
				if v, ok := metricStorage.storage["bytesSent"]; ok {
					bytesSent = v
					delete(metricStorage.storage, "bytesSent")
				}
				if v, ok := metricStorage.storage["sendDuration"]; ok {
					sendDuration = v
					delete(metricStorage.storage, "sendDuration")
				}

				if bytesSent != 0 && sendDuration != 0 {
					gr.SimpleSend(fmt.Sprintf("%s.bytes_to_milliseconds", *graphiteprefixavg), strconv.Itoa(bytesSent/sendDuration))
				}

				for metric, value := range metricStorage.storage {
					gr.SimpleSend(metric, strconv.Itoa(value))
				}
				// clear map
				metricStorage.storage = make(map[string]int)
			}
			ms.mx.Unlock()
			time.Sleep(2 * time.Second)
		}
	}()
}

func (ms *MetricStorage) Increment(name string) {
	ms.mx.Lock()
	defer ms.mx.Unlock()
	ms.storage[name]++
}

func (ms *MetricStorage) Update(name string, value int) {
	ms.mx.Lock()
	defer ms.mx.Unlock()
	if oldValue, ok := ms.storage[name]; !ok {
		ms.storage[name] = value
	} else {
		ms.storage[name] = oldValue + value
	}
}
