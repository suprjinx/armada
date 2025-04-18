package server

import (
	"fmt"
	"net"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/google/uuid"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/extra/redisprometheus/v9"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/armadaproject/armada/internal/common/armadacontext"
	"github.com/armadaproject/armada/internal/common/auth"
	"github.com/armadaproject/armada/internal/common/compress"
	"github.com/armadaproject/armada/internal/common/database"
	grpcCommon "github.com/armadaproject/armada/internal/common/grpc"
	"github.com/armadaproject/armada/internal/common/health"
	log "github.com/armadaproject/armada/internal/common/logging"
	"github.com/armadaproject/armada/internal/common/pulsarutils"
	controlplaneeventspulsarutils "github.com/armadaproject/armada/internal/common/pulsarutils/controlplaneevents"
	"github.com/armadaproject/armada/internal/common/pulsarutils/jobsetevents"
	"github.com/armadaproject/armada/internal/scheduler/reports"
	"github.com/armadaproject/armada/internal/server/configuration"
	"github.com/armadaproject/armada/internal/server/event"
	"github.com/armadaproject/armada/internal/server/executor"
	"github.com/armadaproject/armada/internal/server/queryapi"
	"github.com/armadaproject/armada/internal/server/queue"
	"github.com/armadaproject/armada/internal/server/submit"
	"github.com/armadaproject/armada/pkg/api"
	"github.com/armadaproject/armada/pkg/api/schedulerobjects"
	"github.com/armadaproject/armada/pkg/armadaevents"
	"github.com/armadaproject/armada/pkg/client"
	"github.com/armadaproject/armada/pkg/controlplaneevents"
)

func Serve(ctx *armadacontext.Context, config *configuration.ArmadaConfig, healthChecks *health.MultiChecker) error {
	log.Info("Armada server starting")
	defer log.Info("Armada server shutting down")

	// We call startupCompleteCheck.MarkComplete() when all services have been started.
	startupCompleteCheck := health.NewStartupCompleteChecker()
	healthChecks.Add(startupCompleteCheck)

	// Run all services within an errgroup to propagate errors between services.
	// Defer cancelling the parent context to ensure the errgroup is cancelled on return.
	ctx, cancel := armadacontext.WithCancel(ctx)
	defer cancel()
	g, ctx := armadacontext.ErrGroup(ctx)

	// List of services to run concurrently.
	// Because we want to start services only once all input validation has been completed,
	// we add all services to a slice and start them together at the end of this function.
	var services []func() error

	if err := validateSubmissionConfig(config.Submission); err != nil {
		return err
	}

	// We support multiple simultaneous authentication services (e.g., username/password  OpenId).
	// For each gRPC request, we try them all until one succeeds, at which point the process is
	// short-circuited.
	authServices, err := auth.ConfigureAuth(config.Auth)
	if err != nil {
		return err
	}
	grpcServer := grpcCommon.CreateGrpcServer(config.Grpc.KeepaliveParams, config.Grpc.KeepaliveEnforcementPolicy, authServices, config.Grpc.Tls)

	// Shut down grpcServer if the context is cancelled.
	// Give the server 5 seconds to shut down gracefully.
	services = append(services, func() error {
		<-ctx.Done()
		go func() {
			time.Sleep(5 * time.Second)
			grpcServer.Stop()
		}()
		grpcServer.GracefulStop()
		return nil
	})

	// Create database connection. This is used for the query api, queues and for job deduplication
	dbPool, err := database.OpenPgxPool(config.Postgres)
	if err != nil {
		return errors.WithMessage(err, "error creating postgres pool")
	}
	defer dbPool.Close()
	queryapiServer := queryapi.New(
		dbPool,
		config.QueryApi.MaxQueryItems,
		func() compress.Decompressor { return compress.NewZlibDecompressor() })
	api.RegisterJobsServer(grpcServer, queryapiServer)

	eventDb := createRedisClient(&config.EventsApiRedis)
	defer func() {
		if err := eventDb.Close(); err != nil {
			log.WithError(err).Error("failed to close events api Redis client")
		}
	}()
	prometheus.MustRegister(
		redisprometheus.NewCollector("armada", "events_redis", eventDb))

	queueRepository := queue.NewPostgresQueueRepository(dbPool)
	queueCache := queue.NewCachedQueueRepository(queueRepository, config.QueueCacheRefreshPeriod)
	services = append(services, func() error {
		return queueCache.Run(ctx)
	})
	eventRepository := event.NewEventRepository(eventDb)

	authorizer := auth.NewAuthorizer(
		auth.NewPrincipalPermissionChecker(
			config.Auth.PermissionGroupMapping,
			config.Auth.PermissionScopeMapping,
			config.Auth.PermissionClaimMapping,
		),
	)

	serverId := uuid.New()
	var pulsarClient pulsar.Client
	// API endpoints that generate Pulsar messages.
	pulsarClient, err = pulsarutils.NewPulsarClient(&config.Pulsar)
	if err != nil {
		return err
	}
	defer pulsarClient.Close()
	preProcessor := jobsetevents.NewPreProcessor(config.Pulsar.MaxAllowedEventsPerMessage, config.Pulsar.MaxAllowedMessageSize)
	jobSetEventsPublisher, err := pulsarutils.NewPulsarPublisher[*armadaevents.EventSequence](
		pulsarClient,
		pulsar.ProducerOptions{
			Name:             fmt.Sprintf("armada-server-%s", serverId),
			CompressionType:  config.Pulsar.CompressionType,
			CompressionLevel: config.Pulsar.CompressionLevel,
			BatchingMaxSize:  config.Pulsar.MaxAllowedMessageSize,
			Topic:            config.Pulsar.JobsetEventsTopic,
		},
		preProcessor,
		jobsetevents.RetrieveKey,
		config.Pulsar.SendTimeout,
	)
	if err != nil {
		return errors.Wrapf(err, "error creating pulsar producer for jobset events")
	}
	defer jobSetEventsPublisher.Close()

	controlPlaneEventsPublisher, err := pulsarutils.NewPulsarPublisher[*controlplaneevents.Event](
		pulsarClient,
		pulsar.ProducerOptions{
			Name:             fmt.Sprintf("armada-server-%s", serverId),
			CompressionType:  config.Pulsar.CompressionType,
			CompressionLevel: config.Pulsar.CompressionLevel,
			BatchingMaxSize:  config.Pulsar.MaxAllowedMessageSize,
			Topic:            config.Pulsar.ControlPlaneEventsTopic,
		},
		controlplaneeventspulsarutils.PreProcess[*controlplaneevents.Event],
		controlplaneeventspulsarutils.RetrieveKey[*controlplaneevents.Event],
		config.Pulsar.SendTimeout,
	)
	if err != nil {
		return errors.Wrapf(err, "error creating pulsar producer for control plane events")
	}
	defer controlPlaneEventsPublisher.Close()

	queueServer := queue.NewServer(controlPlaneEventsPublisher, queueRepository, authorizer)

	submitServer := submit.NewServer(
		queueServer,
		jobSetEventsPublisher,
		queueCache,
		config.Submission,
		submit.NewDeduplicator(dbPool),
		authorizer)

	schedulerApiConnection, err := createApiConnection(config.SchedulerApiConnection)
	if err != nil {
		return errors.Wrapf(err, "error creating connection to scheduler api")
	}
	schedulerApiReportsClient := schedulerobjects.NewSchedulerReportingClient(schedulerApiConnection)
	schedulingReportsServer := reports.NewProxyingSchedulingReportsServer(schedulerApiReportsClient)

	eventServer := event.NewEventServer(
		authorizer,
		eventRepository,
		queueCache,
	)

	executorServer := executor.New(controlPlaneEventsPublisher, authorizer)

	api.RegisterSubmitServer(grpcServer, submitServer)
	api.RegisterEventServer(grpcServer, eventServer)
	api.RegisterQueueServiceServer(grpcServer, queueServer)
	api.RegisterExecutorServer(grpcServer, executorServer)

	schedulerobjects.RegisterSchedulerReportingServer(grpcServer, schedulingReportsServer)

	// Cancel the errgroup if grpcServer.Serve returns an error.
	log.Infof("Armada gRPC server listening on %d", config.GrpcPort)
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", config.GrpcPort))
	if err != nil {
		return errors.WithStack(err)
	}
	services = append(services, func() error {
		return grpcServer.Serve(lis)
	})

	// Start all services and wait for the context to be cancelled,
	// which occurs when the parent context is cancelled or if any of the services returns an error.
	// We start all services at the end of the function to ensure all services are ready.
	for _, service := range services {
		g.Go(service)
	}

	startupCompleteCheck.MarkComplete()
	return g.Wait()
}

func createRedisClient(config *redis.UniversalOptions) redis.UniversalClient {
	return redis.NewUniversalClient(config)
}

func validateSubmissionConfig(config configuration.SubmissionConfig) error {
	// Check that the default priority class is allowed to be submitted.
	if config.DefaultPriorityClassName != "" {
		if !config.AllowedPriorityClassNames[config.DefaultPriorityClassName] {
			return errors.WithStack(fmt.Errorf(
				"defaultPriorityClassName %s is not allowed; allowedPriorityClassNames is %v",
				config.DefaultPriorityClassName, config.AllowedPriorityClassNames,
			))
		}
	}
	return nil
}

func createApiConnection(connectionDetails client.ApiConnectionDetails) (*grpc.ClientConn, error) {
	clientMetrics := grpc_prometheus.NewClientMetrics(
		grpc_prometheus.WithClientHandlingTimeHistogram(),
	)
	prometheus.MustRegister(clientMetrics)
	return client.CreateApiConnectionWithCallOptions(
		&connectionDetails,
		[]grpc.CallOption{},
		grpc.WithChainUnaryInterceptor(clientMetrics.UnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(clientMetrics.StreamClientInterceptor()),
	)
}
