

<p align="center">
    proxyhouse
</p>


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

## Contact

Vadim Kulibaba [@recoilme](https://github.com/recoilme)

## License

`b52` source code is available under the MIT [License](/LICENSE).

