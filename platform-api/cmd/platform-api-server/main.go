package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-logr/stdr"
	_ "github.com/lib/pq"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/postgres"
	spannerbackend "github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/spanner"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"
	"github.com/openshift-online/gecko/platform-api/pkg/authz"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

func main() {
	var (
		address         string
		privatePort     int
		publicPort      int
		corsOrigins     string
		enablePublic    bool
		tlsCertFile     string
		tlsKeyFile      string
		authnKubeconfig string
		authzKubeconfig string
		disableAuth     bool
		migrateOnly     bool
	)

	flag.StringVar(&address, "address", "0.0.0.0", "address to bind to")
	flag.IntVar(&privatePort, "private-port", 8080, "port for private API")
	flag.IntVar(&publicPort, "public-port", 8081, "port for public API")
	flag.BoolVar(&enablePublic, "enable-public-api", true, "enable public API server")
	flag.StringVar(&corsOrigins, "cors-origins", "*", "comma-separated list of allowed CORS origins")
	flag.StringVar(&tlsCertFile, "tls-cert-file", "", "path to TLS certificate (auto-generated if empty)")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "", "path to TLS private key (auto-generated if empty)")
	flag.StringVar(&authnKubeconfig, "authentication-kubeconfig", "", "kubeconfig for delegated authentication (in-cluster if empty)")
	flag.StringVar(&authzKubeconfig, "authorization-kubeconfig", "", "kubeconfig for delegated authorization (in-cluster if empty)")
	flag.BoolVar(&disableAuth, "disable-auth", false, "disable authentication/authorization (for testing/local dev)")
	flag.BoolVar(&migrateOnly, "migrate-only", false, "run Spanner DDL migrations and exit (for PreSync Jobs)")
	flag.Parse()

	logger := stdr.New(nil)

	// --migrate-only: run Spanner DDL migrations with retry and exit.
	// Designed for use as an ArgoCD PreSync Job with elevated IAM
	// permissions (roles/spanner.databaseAdmin) so the Deployment can
	// run with only roles/spanner.databaseUser.
	if migrateOnly {
		spannerDB := os.Getenv("SPANNER_DATABASE")
		if spannerDB == "" {
			log.Fatal("--migrate-only requires SPANNER_DATABASE to be set")
		}
		tablePrefix := os.Getenv("SPANNER_TABLE_PREFIX")

		log.Printf("Running Spanner DDL migrations: %s", spannerDB)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := spannerbackend.RunMigrations(ctx, spannerDB, tablePrefix, nil, logger); err != nil {
			log.Fatalf("Spanner DDL migration failed: %v", err)
		}

		log.Println("Spanner DDL migrations completed successfully")
		os.Exit(0)
	}

	// Parse CORS origins
	origins := []string{}
	if corsOrigins != "" {
		origins = strings.Split(corsOrigins, ",")
		for i, origin := range origins {
			origins[i] = strings.TrimSpace(origin)
		}
	}

	// Configure storage backend
	// Priority: Spanner (SPANNER_DATABASE set) > PostgreSQL (DB_HOST set) > in-memory (default)
	var storageFactory apiserver.StorageFactory
	if spannerDB := os.Getenv("SPANNER_DATABASE"); spannerDB != "" {
		// SPANNER_DATABASE must be a full Spanner database resource path:
		//   projects/<project>/instances/<instance>/databases/<database>
		// Set SPANNER_EMULATOR_HOST to redirect to a local emulator, e.g.:
		//   SPANNER_EMULATOR_HOST=localhost:9010
		tablePrefix := os.Getenv("SPANNER_TABLE_PREFIX")

		// SKIP_DDL_SETUP is set by the Helm chart when an ArgoCD PreSync
		// Job handles DDL migrations. The Job runs with
		// roles/spanner.databaseAdmin; the Deployment only needs
		// roles/spanner.databaseUser for data operations.
		skipDDL := os.Getenv("SKIP_DDL_SETUP") == "true"
		if skipDDL {
			log.Println("Skipping DDL setup (handled by PreSync Job)")
		}

		log.Printf("Connecting to Spanner database: %s", spannerDB)

		factory, err := spannerbackend.NewStorageFactory(spannerbackend.StorageFactoryConfig{
			Database:    spannerDB,
			TablePrefix: tablePrefix,
			Context:     context.Background(),
			Logger:      logger,
			SkipDDL:     skipDDL,
		})
		if err != nil {
			log.Fatalf("Failed to create Spanner storage factory: %v", err)
		}

		log.Printf("Connected to Spanner: %s", spannerDB)
		storageFactory = factory
	} else if dbHost := os.Getenv("DB_HOST"); dbHost != "" {
		dbPort := os.Getenv("DB_PORT")
		if dbPort == "" {
			dbPort = "5432"
		}
		dbName := os.Getenv("DB_NAME")
		if dbName == "" {
			dbName = "orlop"
		}
		dbUser := os.Getenv("DB_USER")
		if dbUser == "" {
			dbUser = "orlop"
		}
		dbPassword := os.Getenv("DB_PASSWORD")
		dbSSLMode := os.Getenv("DB_SSLMODE")
		if dbSSLMode == "" {
			dbSSLMode = "disable"
		}

		connStr := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
			dbHost, dbPort, dbName, dbUser, dbPassword, dbSSLMode)

		db, err := sql.Open("postgres", connStr)
		if err != nil {
			log.Fatalf("Failed to open database: %v", err)
		}
		defer db.Close()

		if err := db.Ping(); err != nil {
			log.Fatalf("Failed to connect to database: %v", err)
		}

		log.Printf("Connected to PostgreSQL at %s:%s/%s", dbHost, dbPort, dbName)

		storageFactory = postgres.NewStorageFactory(postgres.StorageFactoryConfig{
			DB:         db,
			ConnString: connStr,
			Context:    context.Background(),
		})
	} else {
		storageFactory = func(resourceType string, scheme *runtime.Scheme, gvk runtimeschema.GroupVersionKind) (storage.ResourceStore, error) {
			return memory.NewMemoryStore(resourceType, scheme, gvk), nil
		}
		log.Println("No SPANNER_DATABASE or DB_HOST set, using in-memory storage")
	}

	// Create a memoized storage factory shared by both the authorizer and
	// the server. The server's internal sharedFactory will double-memoize,
	// which is harmless — identical keys return the same store.
	var (
		storesMu sync.Mutex
		stores   = make(map[string]storage.ResourceStore)
	)
	sharedFactory := func(resourceType string, s *runtime.Scheme, gvk runtimeschema.GroupVersionKind) (storage.ResourceStore, error) {
		storesMu.Lock()
		defer storesMu.Unlock()
		key := resourceType + "/" + gvk.Group + "/" + gvk.Kind
		if st, ok := stores[key]; ok {
			return st, nil
		}
		st, err := storageFactory(resourceType, s, gvk)
		if err != nil {
			return nil, err
		}
		stores[key] = st
		return st, nil
	}

	// Build authz stores from the shared factory using the private scheme.
	privateScheme := getPrivateScheme()
	authzStores, err := buildAuthzStores(sharedFactory, privateScheme)
	if err != nil {
		log.Fatalf("Failed to build authz stores: %v", err)
	}

	// Create the Cedar authorizer (loads roles from stores at startup).
	authorizer, err := authz.NewAuthorizer(context.Background(), authzStores)
	if err != nil {
		log.Fatalf("Failed to create authorizer: %v", err)
	}

	// Set validator dependencies for role/binding validation.
	privatev1.SetValidatorDeps(privatev1.ValidatorDeps{
		RoleExists: func(ctx context.Context, namespace, name string) bool {
			_, err := authzStores.Roles.Get(ctx, namespace, name)
			return err == nil
		},
		PlatformRoleExists: func(ctx context.Context, name string) bool {
			_, err := authzStores.PlatformRoles.Get(ctx, "", name)
			return err == nil
		},
	})

	// Build middleware chain for the public API.
	authnMW := authn.Middleware(disableAuth)
	authzMW := authz.Middleware(authorizer, disableAuth)
	publicMiddleware := []func(http.Handler) http.Handler{authnMW, authzMW}

	// Create server with resource configuration
	opts := apiserver.Options{
		Address: address,
		Private: apiserver.PrivateAPIOptions{
			Port:                     privatePort,
			Resources:                getPrivateResources(),
			Scheme:                   privateScheme,
			TLSCertFile:              tlsCertFile,
			TLSKeyFile:               tlsKeyFile,
			AuthenticationKubeconfig: authnKubeconfig,
			AuthorizationKubeconfig:  authzKubeconfig,
			DisableAuth:              disableAuth,
		},
		Public: apiserver.PublicAPIOptions{
			Enable:     enablePublic,
			Port:       publicPort,
			Resources:  getPublicResources(),
			Scheme:     getPublicScheme(),
			Middleware: publicMiddleware,
		},
		StorageFactory: sharedFactory,
		CORSOrigins:    origins,
		Logger:         logger,
	}

	server, err := apiserver.New(opts)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start policy hot-reload watchers.
	go authorizer.StartWatching(ctx)

	// Start server in goroutine
	go func() {
		if err := server.Run(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for signal
	<-sigChan
	cancel()
	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}
