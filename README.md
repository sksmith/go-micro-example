# Go Micro Template

A microservice template.

## TODO 

* Make sure the application can run from scratch using docker compose
* A link to the parent project
* How do you run the project?
* How do you build the project?
* Add a link to the swagger file
* Lookup a nice README to emulate

## Running the Project

If you wish to see how this application runs in its complete constellation, see [the parent repo](https://github.com/sksmith/smithmfg).

If you just want to run this specific microservice locally...

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