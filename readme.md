```
toolkit
├── config
│   ├── viper.go
│   └── module.go
├── contexts
│   └── (your current file unchanged)
├── errors
│   └── (unchanged)
├── httpclient
│   ├── client.go          (your current BaseClient)
│   └── module.go
├── jwt
│   ├── verifier.go        (current file)
│   └── module.go
├── logging
│   ├── logger.go          (current file)
│   └── module.go
├── messaging
│   ├── nats.go            (NewNATSConn)
│   ├── publisher.go
│   └── module.go
├── middlewares
│   ├── context.go
│   ├── logger.go
│   └── module.go
├── validation
│   ├── validator.go
│   └── module.go
├── utils                  (pagination, uuid helpers … unchanged)
└── fx                     (aggregator)
    └── toolkit.go

```