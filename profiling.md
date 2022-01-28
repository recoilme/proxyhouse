apache benchmark:
```
goos: darwin
goarch: amd64
cpu: Intel(R) Core(TM) i7-8569U CPU @ 2.80GHz
```

```
ab -n 100000 -c 1 -k -p testdata/test_ids.txt http://127.0.0.1:8124/\?query\=INSERT%20INTO%20t%20VALUES
```

```
Concurrency Level:      1
Time taken for tests:   9.079 seconds
Complete requests:      100000
Failed requests:        0
Keep-Alive requests:    100000
Total transferred:      18100000 bytes
Total body sent:        19400000
HTML transferred:       0 bytes
Requests per second:    11014.28 [#/sec] (mean)
Time per request:       0.091 [ms] (mean)
Time per request:       0.091 [ms] (mean, across all concurrent requests)
Transfer rate:          1946.86 [Kbytes/sec] received
                        2086.69 kb/s sent
                        4033.55 kb/s total
```


```
ab -n 100000 -c 10 -k -p testdata/test_ids.txt http://127.0.0.1:8124/\?query\=INSERT%20INTO%20t%20VALUES
```
```
Concurrency Level:      10
Time taken for tests:   2.027 seconds
Complete requests:      100000
Failed requests:        0
Keep-Alive requests:    100000
Total transferred:      18100000 bytes
Total body sent:        19400000
HTML transferred:       0 bytes
Requests per second:    49328.08 [#/sec] (mean)
Time per request:       0.203 [ms] (mean)
Time per request:       0.020 [ms] (mean, across all concurrent requests)
Transfer rate:          8719.12 [Kbytes/sec] received
                        9345.36 kb/s sent
                        18064.48 kb/s total

```