CREATE TABLE products
(
    sku  VARCHAR(50) PRIMARY KEY,
    upc  VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100)       NOT NULL
);

CREATE TABLE product_inventory
(
    sku       VARCHAR(50) PRIMARY KEY REFERENCES products(sku),
    available INTEGER
);

CREATE TABLE production_events
(
    id         INTEGER PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    request_id VARCHAR(100) UNIQUE NOT NULL,
    sku        VARCHAR(50) REFERENCES products(sku),
    quantity   INTEGER,
    created    TIMESTAMP WITH TIME ZONE
);

CREATE
INDEX prod_evt_req_idx ON production_events (request_id);

CREATE TABLE reservations
(
    id                 INTEGER PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    request_id         VARCHAR(100) UNIQUE NOT NULL,
    requester          VARCHAR(100),
    sku                VARCHAR(50) REFERENCES products (sku),
    state              VARCHAR(50),
    reserved_quantity  INTEGER,
    requested_quantity INTEGER,
    created            TIMESTAMP WITH TIME ZONE
);

CREATE
INDEX res_sku_state_idx ON reservations (sku, state);

CREATE
INDEX res_req_id_idx ON reservations (request_id);

COMMIT;