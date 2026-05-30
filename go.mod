module github.com/marendonq/distributed-ec2-autoscaler

go 1.25.0

require google.golang.org/grpc v1.81.1

require (
	github.com/lib/pq v1.10.6
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 // indirect
)

replace google.golang.org/genproto => github.com/googleapis/go-genproto v0.0.0-20230410155749-daa745c078e1

replace golang.org/x/xerrors => github.com/golang/xerrors v0.0.0-20191204190536-9bdfabe68543
