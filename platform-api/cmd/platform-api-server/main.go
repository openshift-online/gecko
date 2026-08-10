package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"net/http"

	"github.com/go-logr/stdr"
	_ "github.com/lib/pq"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/postgres"
	spannerbackend "github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/spanner"
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
		authzConfigDir  string
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
	flag.StringVar(&authzConfigDir, "authz-config", "/etc/gecko/authz", "path to authz ConfigMap mount (roles.yaml, bootstrap.yaml)")
	flag.Parse()

	logger := stdr.New(nil)

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

		log.Printf("Connecting to Spanner database: %s", spannerDB)

		factory, err := spannerbackend.NewStorageFactory(spannerbackend.StorageFactoryConfig{
			Database:    spannerDB,
			TablePrefix: tablePrefix,
			Context:     context.Background(),
			Logger:      logger,
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

	// Load authz configuration (roles and bootstrap bindings) from ConfigMap.
	// This is loaded before server creation so policies are ready when the
	// middleware starts processing requests.
	var roleConfig *authz.RoleConfig
	var authorizer *authz.Authorizer
	if !disableAuth {
		var err error
		roleConfig, err = authz.LoadConfig(authzConfigDir)
		if err != nil {
			log.Printf("Warning: failed to load authz config from %s: %v (authorization will deny all requests)", authzConfigDir, err)
			// Create a minimal config with no roles — all requests will be denied
			roleConfig = &authz.RoleConfig{
				RoleLabels:          make(map[string]bool),
				NamespaceRoleLabels: make(map[string]bool),
				PlatformRoleLabels:  make(map[string]bool),
			}
		} else {
			log.Printf("Loaded %d roles from authz config", len(roleConfig.Roles))
			// Set the global role ref validator so RoleBinding/PlatformRoleBinding
			// types can validate roleRef against known roles.
			authz.SetRoleValidator(roleConfig)
			privatev1.RoleRefValidator = func(roleRef, scope string) error {
				if scope == "namespace" {
					return authz.ValidateNamespaceRoleRef(roleRef)
				}
				return authz.ValidatePlatformRoleRef(roleRef)
			}
		}

		// Generate Cedar policies from role definitions
		policies, err := authz.GeneratePolicies(roleConfig.Roles)
		if err != nil {
			log.Fatalf("Failed to generate Cedar policies: %v", err)
		}

		// Create entity getter and authorizer (stores will be set after server creation)
		cache := authz.NewEntityCache()
		entityGetter := authz.NewEntityGetter(nil, nil, cache)
		authorizer = authz.NewAuthorizer(policies, entityGetter)
	}

	// Build public API middleware chain.
	// When auth is disabled (local dev), use DevModeMiddleware instead of the
	// ESPv2-based authn middleware.
	var publicMiddleware []func(http.Handler) http.Handler
	if !disableAuth {
		publicMiddleware = append(publicMiddleware, authn.Middleware)
		publicMiddleware = append(publicMiddleware, authz.NewMiddleware(authorizer))
	} else {
		publicMiddleware = append(publicMiddleware, authn.DevModeMiddleware(""))
		log.Println("Public API auth disabled: using dev mode identity")
	}

	// Create server with resource configuration
	opts := apiserver.Options{
		Address: address,
		Private: apiserver.PrivateAPIOptions{
			Port:                     privatePort,
			Resources:                getPrivateResources(),
			Scheme:                   getPrivateScheme(),
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
		StorageFactory: storageFactory,
		CORSOrigins:    origins,
		Logger:         logger,
	}

	server, err := apiserver.New(opts)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Wire authz stores now that the server is created and stores are available.
	if !disableAuth && server.PublicRegistry() != nil {
		rbStore := server.PublicRegistry().GetStore(runtimeschema.GroupKind{
			Group: "gcp.managed.openshift.io", Kind: "RoleBinding"})
		prbStore := server.PublicRegistry().GetStore(runtimeschema.GroupKind{
			Group: "gcp.managed.openshift.io", Kind: "PlatformRoleBinding"})

		if rbStore != nil && prbStore != nil {
			// Update the entity getter with the actual stores
			cache := authz.NewEntityCache()
			entityGetter := authz.NewEntityGetter(rbStore, prbStore, cache)
			authorizer.SetEntityGetter(entityGetter)
			log.Println("Authorization stores wired successfully")
		} else {
			log.Println("Warning: could not retrieve authz stores from registry")
		}

		// Run bootstrap: upsert initial PlatformRoleBindings from config
		if roleConfig != nil && len(roleConfig.BootstrapBindings) > 0 && prbStore != nil {
			if err := authz.RunBootstrap(context.Background(), prbStore, roleConfig.BootstrapBindings); err != nil {
				log.Printf("Warning: bootstrap failed: %v", err)
			}
		}
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		if err := server.Run(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for signal
	<-sigChan
	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}
