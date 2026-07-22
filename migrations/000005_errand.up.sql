CREATE TABLE errand_tasks (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, title VARCHAR(200) NOT NULL, description TEXT NOT NULL,
    reward_cents BIGINT NOT NULL, currency VARCHAR(3) NOT NULL, pickup_location VARCHAR(500) NOT NULL,
    dropoff_location VARCHAR(500) NOT NULL, deadline DATETIME(3) NOT NULL, status VARCHAR(24) NOT NULL,
    requester_id BIGINT UNSIGNED NOT NULL, runner_id BIGINT UNSIGNED NULL, trade_order_id BIGINT UNSIGNED NULL,
    accepted_at DATETIME(3) NULL, picked_up_at DATETIME(3) NULL, delivered_at DATETIME(3) NULL,
    completed_at DATETIME(3) NULL, cancelled_at DATETIME(3) NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_errand_trade_order (trade_order_id), KEY idx_errand_tasks_status (status),
    KEY idx_errand_tasks_deadline (deadline), KEY idx_errand_tasks_requester (requester_id), KEY idx_errand_tasks_runner (runner_id),
    KEY idx_errand_open_deadline (status, deadline),
    CONSTRAINT fk_errand_trade_order FOREIGN KEY (trade_order_id) REFERENCES trade_orders(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE errand_transitions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, task_id BIGINT UNSIGNED NOT NULL, from_status VARCHAR(24) NOT NULL,
    to_status VARCHAR(24) NOT NULL, actor_id BIGINT UNSIGNED NULL, reason VARCHAR(500) NULL,
    idempotency_key VARCHAR(128) NOT NULL, created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_errand_transition_idempotency (idempotency_key), KEY idx_errand_transition_task (task_id),
    CONSTRAINT fk_errand_transition_task FOREIGN KEY (task_id) REFERENCES errand_tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
