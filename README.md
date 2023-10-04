# Temporal cloud api proto files

## How to use

Copy over the protobuf files under [temporal](temporal) directory to the project directory and then use [grpc](https://grpc.io/docs/) to compile and generate code in the desired programming language.

### API Version

The client is expected to pass in a `temporal-cloud-api-version` header with the api version identifier with every request it makes to the apis. The backend will use the version to safely mutate resources.

Current Version:

https://github.com/temporalio/api-cloud/blob/144acf180aedb131abdfd7c1f0759b0826467fd5/VERSION#L1C1-L1C14

### URL

The grpc URL the clients should connect to:
```
saas-api.tmprl.cloud:443
```

## Samples

Refer [cloud-samples-go](https://github.com/temporalio/cloud-samples-go) repository for demonstration on how a project can copy and build Go clients.
