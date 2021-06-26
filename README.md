# Go Micro Template

This small sample project was created as a collection of the various things I've learned about best
practices building applications using Go. I structured the project using a hexagonal style abstracting
away business logic from dependencies like the RESTful API, the Postgres, and RabbitMQ.

## Structure

The Go community generally likes application directory structures to be as simple as possible which is
totally admirable and applicable for a small simple microservice. I could probably have kept everything
for this project in a single directory and focused on making sure it met twelve factors. But I'm a big
fan of [Domain Driven Design](https://martinfowler.com/bliki/DomainDrivenDesign.html), and how it gels so
nicely with [Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/) and I wanted
to see how a Go microservice might look structured using them.

The starting point of the application is under the [cmd/app](cmd/app/main.go) directory. The "domain"
core of the application where all business logic should reside is under the [internal/core](internal/core)
directory. The other directories listed there are each of the external dependencies for the project.

![structure diagram](inventory.jpg)

## Running the Application Locally

This project requires that you have Docker, Go and Make installed locally. If you do, you can start
the application first by starting the docker-compose file, then start the application using the
supplied Makefile.

```shell
docker-compose -f ./scripts/docker-compose.yml up -d
make run
```

If you want to create a deployable executable and run it:

```shell
make build
./bin/inventory
```

### Run Docker Compose

```shell
docker-compose up
```

## Database Migrations

I'm using the migrate project to manage database migrations.

```shell
migrate create -ext sql -dir db/migrations -seq create_products_table

migrate -database postgres://postgres:postgres@localhost:5432/smfg-db?sslmode=disable -path db/migrations up

migrate -source file://db/migrations -database postgres://localhost:5432/database down
```

## 12 Factors

One of the goals of this service was to ensure all [12 principals](https://12factor.net/) of a 12-factor 
app are adhered to. This was a nice way to make sure the app I built offered most of what you need out of
a Spring Boot application.

### I. Codebase

The application is stored in my git repository.

### II. Dependencies

Go handles this for us through its dependency management system (yay!)

### III. Config

The app initially starts up using environment variables that define a URL and credentials for connecting
to a [Spring Cloud Config](https://github.com/spring-cloud/spring-cloud-config) service. This allows for
the externalization of configurations which are managed through a typical version control system. Pretty
nice! [I wrote a basic library](https://github.com/sksmith/go-spring-config) for connecting to and reading
configurations from Spring Cloud Config since I'm mostly writing Spring Boot microservices these days.

### IV. Backing Services

The application connects to all external dependencies (in this case, RabbitMQ, and Postgres) via URLs which
it gets from remote configuration.

### V. Build, release, run

The application can easily be plugged into any CI/CD pipeline. This is mostly thanks to Go making this easy
through great command line tools.

### VI. Processes

This app is not *strictly* stateless. There is a cache in the user repository. This was a design choice I
made in the interest of seeing what setting up a local cache in go might look like. In a more real-world
application you would probably want an external cache (like Redis), or a distributed cache 
(like [Group Cache](https://github.com/golang/groupcache) - which is really cool!)

This app is otherwise stateless and threadsafe.

### VII. Port Binding

The application binds to a supplied port on startup.

### VIII. Concurrency

Other than maintaining an instance-based cache (see Process above), the application will scale horizontally 
without issue. The database dependency would need to scale vertically unless you started using sharding, or
a distributed data store like [Cosmos DB](https://docs.microsoft.com/en-us/azure/cosmos-db/distribute-data-globally).

### IX. Disposability

One of the wonderful things about Go is how *fast* it starts up. This application can start up and shut down
in a fraction of the time that similar Spring Boot microservices. In addition they use a much smaller footprint.
This is perfect for services that need to be highly elastic on demand.

### X. Dev/Prod Parity

Docker makes standing up a prod-like environment on your local environment a breeze. This application has
[a docker-compose file](scripts/docker-compose.yml) that starts up a local instance of rabbit and postgres.
This obviously doesn't account for ensuring your dev and stage environments are up to snuff but at least
that's a good start for local development.

### XI. Logs

Logs in the application are written to the stdout allowing for logscrapers like 
[logstash](https://www.elastic.co/logstash) to consume and parse the logs. Through configuration the logs
can output as plain text for ease of reading during local development and then switched after deployment
into json structured logs for automatic parsing.

### XII. Admin Processes

Database migration is automated in the project using [migrate](https://github.com/golang-migrate/migrate).
