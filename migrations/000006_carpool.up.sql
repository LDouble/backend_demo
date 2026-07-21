CREATE TABLE carpool_trips (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, title VARCHAR(200) NOT NULL, origin VARCHAR(500) NOT NULL, destination VARCHAR(500) NOT NULL,
    departure_at DATETIME(3) NOT NULL, total_seats BIGINT NOT NULL, occupied_seats BIGINT NOT NULL DEFAULT 0, status VARCHAR(24) NOT NULL,
    organizer_id BIGINT UNSIGNED NOT NULL, contact_type VARCHAR(16) NOT NULL, contact_ciphertext LONGTEXT NOT NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), KEY idx_carpool_open_departure (status, departure_at), KEY idx_carpool_trips_organizer_id (organizer_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE carpool_participants (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, trip_id BIGINT UNSIGNED NOT NULL, user_id BIGINT UNSIGNED NOT NULL, status VARCHAR(16) NOT NULL,
    joined_at DATETIME(3) NOT NULL, cancelled_at DATETIME(3) NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_carpool_trip_user (trip_id,user_id), KEY idx_carpool_participants_status (status),
    CONSTRAINT fk_carpool_participant_trip FOREIGN KEY (trip_id) REFERENCES carpool_trips(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
