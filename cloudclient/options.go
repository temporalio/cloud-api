package cloudclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	defaultCloudOpsAPIHostPort = "saas-api.tmprl.cloud:443"
	defaultAPIVersion          = "test-cr"

	authorizationHeader           = "Authorization"
	authorizationBearer           = "Bearer"
	temporalCloudAPIVersionHeader = "temporal-cloud-api-version"

	sdkVersion = "0.3.2"
)

// Options to configure the cloud operations client.
// The minimum requirement is one of APIKey or APIKeyReader to be set.
// All other options are optional.
type Options struct {
	// The API key to use when making requests to the cloud operations API.
	// At least one of APIKey and APIKeyReader must be provided, but not both.
	APIKey string

	// The API key reader to dynamically retrieve apikey to use when making requests to the cloud operations API.
	// At least one of APIKey and APIKeyReader must be provided, but not both.
	APIKeyReader APIKeyReader

	// The hostport to use when connecting to the cloud operations API.
	// If not provided, the default hostport of `saas-api.tmprl.cloud:443` will be used.
	HostPort string

	// Allow the client to connect to the cloud operations API using an insecure connection.
	// This should only be used for testing purposes.
	AllowInsecure bool

	// The TLS configuration to use when connecting to the cloud operations API.
	// If not provided, a default TLS configuration will be used.
	// Will be ignored if AllowInsecure is set to true.
	TLSConfig *tls.Config

	// The API version to use when making requests to the cloud operations API.
	// If not provided, the latest API version  will be used.
	APIVersion string

	// Disable the default retry policy.
	// If not provided, the default retry policy will be used.
	// The default retry policy is an exponential backoff with jitter with a maximum of 7 retries for retriable errors.
	// The default retry policy will also set the operations id on the write requests, if not already set.
	// This is to ensure the write requests are idempotent in the case of a retry.
	DisableRetry bool

	// UserAgent product information to prepend to the user-agent header. Must follow RFC 9110.
	// If not provided, the user-agent header will contain product and version information for this SDK and grpc.
	UserAgent string

	// Add additional gRPC dial options.
	// This can be used to set custom timeouts, interceptors, etc.
	GRPCDialOptions []grpc.DialOption
}

// APIKeyReader is an interface to dynamically retrieve the API key to use when making requests to the cloud operations API.
type APIKeyReader interface {
	// Get the API key to use when making requests to the cloud operations API.
	// If an error is returned, the request will fail.
	// The GetAPIKey function will be called every time a request is made to the cloud operations API.
	GetAPIKey(ctx context.Context) (string, error)
}

type staticAPIKeyReader struct {
	// The API key to use when making requests to the cloud operations API.
	APIKey string
}

func (r staticAPIKeyReader) GetAPIKey(ctx context.Context) (string, error) {
	return r.APIKey, nil
}

func (o *Options) compute() (
	hostPort string,
	grpcDialOptions []grpc.DialOption,
	err error,
) {
	hostPort = o.HostPort
	if hostPort == "" {
		// set the default host port if not provided
		hostPort = defaultCloudOpsAPIHostPort
	}

	// setup the grpc dial options
	grpcDialOptions = make([]grpc.DialOption, 0, len(o.GRPCDialOptions)+4)

	var transport credentials.TransportCredentials
	// setup the transport
	if o.AllowInsecure {
		// allow insecure transport
		transport = insecure.NewCredentials()
	} else {
		// use the provided tls config, or the zero value if not provided
		transport = credentials.NewTLS(o.TLSConfig)
	}
	grpcDialOptions = append(grpcDialOptions,
		grpc.WithTransportCredentials(transport),
	)

	if o.APIKey != "" && o.APIKeyReader != nil {
		return "", nil, errors.New("only one of APIKey and APIKeyReader can be provided")
	}
	// setup the api key credentials
	creds := apikeyCreds{
		allowInsecureTransport: o.AllowInsecure,
	}
	if o.APIKey != "" {
		creds.reader = staticAPIKeyReader{APIKey: o.APIKey}
	} else if o.APIKeyReader != nil {
		creds.reader = o.APIKeyReader
	}
	if creds.reader == nil {
		return "", nil, errors.New("either APIKey or APIKeyReader must be provided")
	} else {
		grpcDialOptions = append(grpcDialOptions,
			grpc.WithPerRPCCredentials(creds),
		)
	}

	// setup the api version header
	version := o.APIVersion
	if version == "" {
		version = defaultAPIVersion
	}

	// setup user-agent string
	userAgent := fmt.Sprintf("temporalio-cloud-sdk-go/%s", sdkVersion)
	if o.UserAgent != "" {
		userAgent = fmt.Sprintf("%s %s", strings.TrimSpace(o.UserAgent), userAgent)
	}
	grpcDialOptions = append(grpcDialOptions, grpc.WithUserAgent(userAgent))

	grpcDialOptions = append(grpcDialOptions, grpc.WithChainUnaryInterceptor(
		func(
			ctx context.Context,
			method string,
			req interface{}, reply interface{},
			conn *grpc.ClientConn,
			invoker grpc.UnaryInvoker,
			opts ...grpc.CallOption,
		) error {
			ctx = metadata.AppendToOutgoingContext(ctx, temporalCloudAPIVersionHeader, version)
			return invoker(ctx, method, req, reply, conn, opts...)
		},
	))

	if !o.DisableRetry {
		// setup the default retry policy
		retryOpts := []retry.CallOption{
			retry.WithBackoff(
				retry.BackoffExponentialWithJitter(500*time.Millisecond, 0.5),
			),
			retry.WithMax(7),
		}
		grpcDialOptions = append(grpcDialOptions, grpc.WithChainUnaryInterceptor(
			// set the operation id on the write requests, if not already set
			// this will make the write requests idempotent in the case of a retry
			setOperationIDGRPCInterceptor,
			// retry the request on retriable errors
			retry.UnaryClientInterceptor(retryOpts...),
		))
	}

	grpcDialOptions = append(grpcDialOptions, o.GRPCDialOptions...)
	return hostPort, grpcDialOptions, nil
}
