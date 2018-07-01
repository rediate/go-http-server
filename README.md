# go-http-server
simple http server in go

Simple http server implemented with go

To run program:
	go run server.go
	
This server listens on port 8080 of the local host. The following commands are supported:

	POST
	curl - data "password=blah" http://localhost:8080/hash 
	
	GET
	curl http://localhost:8080/stats
	curl http://localhost:8080/hash/$num
	
