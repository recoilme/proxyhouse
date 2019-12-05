

`proxyhouse` WIP!

[![GoDoc](https://img.shields.io/badge/api-reference-blue.svg?style=flat-square)](https://godoc.org/github.com/recoilme/proxyhouse)

proxyhouse is a fast experimental clickhouse proxy.


# Getting Started

## Installing

To start using `proxyhouse`, install Go and run `go get`:

```sh
$ go get -u github.com/recoilme/proxyhouse
$ go build or go install
```

This will retrieve and build the server. Or grab compiled binary version.

## Starting

Use `./proxyhouse --help` for full list of params. Example:

```sh
./proxyhouse -p 11215
# Start server on port 11215
```

or just ./proxyhouse - for start with default params

## How it work

`proxyhouse` is a proxy for insert request in clickhouse.

You send post request:


```$ echo '(1),(2),(3)' | curl 'http://localhost:8123/?query=INSERT%20INTO%20t%20FORMAT%20Values' --data-binary @-```


proxyhouse create map:

`requests['http://localhost:8123/?query=INSERT%20INTO%20t%20FORMAT%20Values']= '(1),(2),(3)'`


You send next request:

```$ echo '(4),(5),(6)' | curl 'http://localhost:8123/?query=INSERT%20INTO%20t%20FORMAT%20Values' --data-binary @-```


proxyhouse add `,` and body value too map:


`requests['http://localhost:8123/?query=INSERT%20INTO%20t%20FORMAT%20Values']= '(1),(2),(3),(4),(5),(6)'`

Every second - proxyhouse flush all gathered requests in clickhouse.

## Example (send 100 req parallel)

```
go test -race
proxyhouse started on port 8124 (loops: 4)
Hello, test
-- bulk --

go version go1.13 darwin/amd64

     number of cpus: 4
     number of inserts: 100
100 ops over 4 threads in 63ms, 1,593/sec, 627624 ns/op, 1.2 MB, 12849 bytes/op
done
store: uri:/?query=INSERT%20INTO%20t%20VALUES
body:(75),(0),(50),(25),(76),(1),(77),(26),(51),(52),(2),(27),(28),(53),(78),(54),(29),(79),(55),(3),(80),(56),(30),(31),(4),(81),(57),(5),(32),(82),(58),(6),(83),(33),(59),(7),(84),(60),(85),(8),(34),(9),(61),(86),(35),(62),(10),(87),(11),(63),(88),(64),(12),(89),(36),(13),(65),(90),(37),(66),(91),(38),(67),(39),(92),(14),(40),(15),(93),(68),(41),(16),(69),(42),(94),(17),(70),(95),(43),(71),(18),(44),(96),(72),(19),(45),(20),(73),(97),(74),(46),(21),(98),(47),(22),(48),(23),(49),(24),(99)
PASS
```

## Contact

Vadim Kulibaba [@recoilme](https://github.com/recoilme)

## License

`proxyhouse` source code is available under the MIT [License](/LICENSE).

