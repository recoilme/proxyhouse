

`proxyhouse`

[![GoDoc](https://img.shields.io/badge/api-reference-blue.svg?style=flat-square)](https://godoc.org/github.com/recoilme/proxyhouse)

proxyhouse is a fast experimental clickhouse proxy.


# Getting Started

## Installing

To start using `proxyhouse`, install Go and run `go get`:

```sh
$ go get -u github.com/recoilme/proxyhouse
$ GOOS=linux go build
```

This will retrieve and build the server. Or grab compiled binary version.

## Starting

Use `./proxyhouse --help` for full list of params. Example:

```sh
./proxyhouse -p 11215
# Start server on port 11215
```

or just ./proxyhouse - for start with default params

## Как это работает

Во-первых, лучше всего читать код, там всего 400 строк. Описание может устареть.


Во-вторых - ниже формализованное, краткое описание на скупом английском, для тех кто не любит лонгриды. 


Итак. Это прокси. Его цель собирать много маленьких запросов в большие. Запускается он так:

`proxyhouse -repl="http://ch2.surfy.ru:8124" -graphitehost="graphite.surfy.ru"`

Все параметры - можно задать в коммандной строке. Ниже они перечислены подробней.

Начнем с http части.


Сервер написан на evio - это низкоуровневая библиотека для работы с сокетами. Потому что мы думали что у нас будут десятки тысячи запросов/секунду и нам нужен очень быстрый сервер, на практике их пока сотни, но что сделано то сделано. Сервер слушает GET запрос на `/status` - его шлет нагиос - чтобы проверить всё ли в порядке. На него он говорит - ок - я тут. На все остальные GET запросы сервер говорит "отвали". Потому что это прокси для вставки в КХ.


POST запросы. Вот эти запросы - это вставка в кликхаус.

POST запрос выглядит как куда вставляем - в uri и что вставляем в BODY. Ниже - есть примеры, которые я не буду дублировать тут чтобы не захламлять занимательное чтиво. Вот. Для хранения и мержинга этих запросов используется map[string]bytes. Не знаю как по-русски написать.

Uri - это ключ, а BODY - складываются по ключу

Есть параметр repl - на что реплэйсить то -куда вставлять,  потому что в uri - может быть всякий тлен. Адрес прокси или без адреса. Кто то шлет с http кто то ip адрес и тп, все это заменится на адрес собственно кликхауса. Парметр forward. Вот код про это:
```
if strings.HasPrefix(uri, "/") {
	uri = *fwd + uri
} else {
	uri = strings.Replace(uri, *repl, *fwd, 1)
}
```

Еще URI приводится к единообразию, там может быть insert%20into%20 - вот так, или вот так insert+into+ - пофиг. Таблица, в которую вставляется извлекается только для мониторинга в графит. В остальном  - прилетел какой то ури, это легло в ключ. Дальше - в BODY запроса - содержался собственно сам запрос. Если Ключ пустой, то он просто создастся, если нет - приджойнится к тому что там было.  Вот код про это:

```
_, ok := store.Req[uri]
if !ok {
	store.Req[uri] = make([]byte, 0, buffersize)
} else {
	store.Req[uri] = append(store.Req[uri], []byte(*delim)...)
}
store.Req[uri] = append(store.Req[uri], body...)
```

Таким образом - сервер слушает чё пришлют и складывает в мапу. Едем дальше, бэкграундменеджер.


В фоне, крутится задача, которая ходит раз в заданное количество секунд (по умолчанию 2) по мапам, берет их, и отправляет на адрес fwd - forward - в кликхаус. Всё. Больше она ниче не делает. Но. Кликхаус может оветить нам 500 кодом, и вообще, что то может пойти не так. Тогда. Вот этот вот совокупный запрос - сохранится в файл. Точнее в два файла. тело запроса/карты  - в текущее время, а ключ запроса - в файл с текущим временем с расширением idx. Вот код про это:
```
	db := fmt.Sprintf("errors/%d", time.Now().UnixNano())
	pudge.Set(db, key, val)
	pudge.Close(db)
```

Пудж - это библиотека которая делает весь тлен связанный с блокироваками - созданием файлов и прочей гадостью. Те он вот эти два файла создаст и туда все запишет. Это называется ошибка.

В заданный в параметрах коммандной строки интервал (по умолчанию 60) - вызывается функция "проверка ошибок" - здесь интервал не в секундах - а в иттерациях, ну типа - если раз в 2 секунды шлем в КХ, то раз в две минуты - проверим не было ли ошибок. Как оно работает.


 filePathWalkDir - вернет список файлов в папке error
Идем по ним в цикле - и отправляем потихоньку, вот код про это:

```
for _, file := range list {
		db, err := pudge.Open("errors/"+file, nil)
		keys, err := db.Keys(nil, 0, 0, true)
		for _, key := range keys {
			err := db.Get(key, &val)
			err = send(string(key), val, false)
		}
		db.DeleteFile()
		// sleep 3 seconds to prevent throttling CH
		time.Sleep(3 * time.Second)
	}
```
Я убрал проверку ошибок (не благодарите), потому что в гоу принято всегда и везде проверять нет ли ошибки, а не игнорить их как в других языках и неподготовленный головной мозг может запутаться. Те до удаления ошибки код не дойдет, если не удалось обработать ошибку. Наверное зря я убрал обработку ошибок, но ничего не мешает посмотреть код и увидеть как все происходит. Вобщем читаем пару файлов, ключ и значение - и шлем в КХ. В цикле ключи - потому что кто знает - может я захочу агрегировать ошибки более крупными пачками. Но в принципе - на момент описания. Ошибка это два файла - uri/ключ и body/значение. Слип там - чтобы отрабатывать плавненько ошибки и не нарваться на 500 от КХ. Потому что как работает КХ - неизвестно, может он забракует всего клиента и начнет 500ть на всё подряд, поэтому лучше перебдеть чем недобдеть, логика такая. Не рудно видеть - что ошибки это тупо файлы, и могут быть обработаны не в проксе, а снаружи. Их можно мувнуть в другую папку - и например поправить прям в теле или в ключе - если клиент слал какую то гадость и она не может быть обработана - вот это скользкое место всей прокси. Те ее можно засрать какашками и она будет монотонно пытаться их разгрести. Но затротлиться - не должна. Когда вы зайдите в графит и увидите что там ошибки - вы должны сами решить что с ними делать. 


## How it work

`proxyhouse` is a proxy for insert request in clickhouse.

You send post request:


```$ echo '(1),(2),(3)' | curl 'http://localhost:8124/?query=INSERT%20INTO%20t%20FORMAT%20Values' --data-binary @-```


proxyhouse create map:

`requests['/?query=INSERT%20INTO%20t%20FORMAT%20Values']= '(1),(2),(3)'`


You send next request:

```$ echo '(4),(5),(6)' | curl 'http://localhost:8124/?query=INSERT%20INTO%20t%20FORMAT%20Values' --data-binary @-```


proxyhouse add `,` and body value too map:


`requests['/?query=INSERT%20INTO%20t%20FORMAT%20Values']= '(1),(2),(3),(4),(5),(6)'`

Every second - proxyhouse flush all gathered requests in clickhouse.

## Example (send 100 req parallel)

```
Requests: 

	http.NewRequest("POST", "http://127.0.0.1:8124/?query=INSERT%20INTO%20t%20VALUES", (fmt.Sprintf("(%d)", i)))

Result:

uri:
/?query=INSERT%20INTO%20t%20VALUES

body:
(75),(0),(50),(25),(76),(1),(77),(26),(51),(52),(2),(27),(28),(53),(78),(54),(29),(79),(55),(3),(80),(56),(30),(31),(4),(81),(57),(5),(32),(82),(58),(6),(83),(33),(59),(7),(84),(60),(85),(8),(34),(9),(61),(86),(35),(62),(10),(87),(11),(63),(88),(64),(12),(89),(36),(13),(65),(90),(37),(66),(91),(38),(67),(39),(92),(14),(40),(15),(93),(68),(41),(16),(69),(42),(94),(17),(70),(95),(43),(71),(18),(44),(96),(72),(19),(45),(20),(73),(97),(74),(46),(21),(98),(47),(22),(48),(23),(49),(24),(99)
```

## Graphite

Proxyhouse will send to Graphite this metrics:

 - count.proxyhouse.ch_errors //Clickhouse error
 - count.proxyhouse.wrong_requests // wrong request
 - count.proxyhouse.rows_sent // count sended values
 - count.proxyhouse.requests_sent // count sended requests
 - count.proxyhouse.requests_received // count recieved requests

## Failover

In case of errors:

- wrong request (not POST with INSERT)-> send to 400 to client and grafite wrong_requests
- clickhouse is down -> Send to graphite ch_errors count (+1) -> write packets to errors dir (by interval)
- every 60 steps - try to resend packets from errors folder -> on error - silently skip
- on start, if errors folder not empty -> try resend packets from errors folder to clickhouse -> on error - panic (manualy delete errors folder)

## Params

```
	port           = flag.Int("p", 8124, "TCP port number to listen on (default: 8124)")
	unixs          = flag.String("unixs", "", "unix socket")
	stdlib         = flag.Bool("stdlib", false, "use stdlib")
	noudp          = flag.Bool("noudp", true, "disable udp interface")
	workers        = flag.Int("workers", -1, "num workers")
	balance        = flag.String("balance", "random", "balance - random, round-robin or least-connections")
	keepalive      = flag.Int("keepalive", 10, "keepalive connection, in seconds")
	fwd            = flag.String("fwd", "http://localhost:8123", "forward to this server (clickhouse)")
	repl           = flag.String("repl", "http://localhost:8124", "replace this string on forward")
	delim          = flag.String("delim", ",", "body delimiter")
	syncsec        = flag.Int("syncsec", 2, "sync interval, in seconds")
	graphitehost   = flag.String("graphitehost", "", "graphite host")
	graphiteport   = flag.Int("graphiteport", 2023, "graphite port")
	graphiteprefix = flag.String("graphiteprefix", "relap.count.proxyhouse", "graphite prefix")
	isdebug        = flag.Bool("isdebug", false, "debug requests")
	resendint      = flag.Int("resendint", 60, "resend error interval, in steps")
```

## Benchmark

```
go version go1.13.5 darwin/amd64

     number of cpus: 8
     number of inserts: 10000
10,000 ops over 8 threads in 140ms, 71,241/sec, 14036 ns/op, 2.1 MB, 216 bytes/op

```

## Contact

Vadim Kulibaba [@recoilme](https://github.com/recoilme)

## License

`proxyhouse` source code is available under the MIT [License](/LICENSE).

