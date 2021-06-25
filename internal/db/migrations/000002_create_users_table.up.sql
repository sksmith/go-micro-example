CREATE TABLE users
(
    username VARCHAR(50)   PRIMARY KEY,
    password VARCHAR(5000) NOT NULL,
    is_admin BOOLEAN DEFAULT FALSE,
    created_at  TIMESTAMP WITH TIME ZONE
);

INSERT INTO users (username, password, is_admin, created_at)
    VALUES ('admin', '$2a$10$v6K6OZgz.oUPSPGQfiarAOzD6JTz2.e5hdKCkq31NglPnAsT6j1GO', true, NOW());

COMMIT;