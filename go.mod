module github.com/marendonq/distributed-ec2-autoscaler

go 1.20

require google.golang.org/grpc v1.56.0

require github.com/lib/pq v1.10.6

require (
	github.com/golang/protobuf v1.5.3 // indirect
	golang.org/x/net v0.9.0 // indirect
	golang.org/x/sys v0.7.0 // indirect
	golang.org/x/text v0.9.0 // indirect
	google.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 // indirect
	google.golang.org/protobuf v1.30.0 // indirect
)

// Temporary workaround: fetch grpc implementation from GitHub if DNS to
// google.golang.org fails in this environment.
replace google.golang.org/grpc => github.com/grpc/grpc-go v1.56.0

replace google.golang.org/protobuf => github.com/protocolbuffers/protobuf-go v1.30.0

replace google.golang.org/genproto => github.com/googleapis/go-genproto v0.0.0-20230410155749-daa745c078e1

replace golang.org/x/xerrors => github.com/golang/xerrors v0.0.0-20191204190536-9bdfabe68543
