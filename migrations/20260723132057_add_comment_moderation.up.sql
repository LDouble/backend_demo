-- module: comment
CREATE TABLE comments (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  target_type VARCHAR(32) NOT NULL,
  target_id BIGINT UNSIGNED NOT NULL,
  author_id BIGINT UNSIGNED NOT NULL,
  parent_id BIGINT UNSIGNED NULL,
  root_id BIGINT UNSIGNED NULL,
  depth BIGINT NOT NULL,
  reply_to_user_id BIGINT UNSIGNED NULL,
  content LONGTEXT NOT NULL,
  status VARCHAR(24) NOT NULL,
  review_reason VARCHAR(500) NULL,
  reviewed_by BIGINT UNSIGNED NULL,
  reviewed_at DATETIME(3) NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_comment_target_roots (target_type, target_id, parent_id, status),
  KEY idx_comment_root_tree (root_id, depth),
  KEY idx_comment_author_status (author_id, status),
  CONSTRAINT fk_comments_parent_id FOREIGN KEY (parent_id) REFERENCES comments(id) ON DELETE CASCADE,
  CONSTRAINT fk_comments_root_id FOREIGN KEY (root_id) REFERENCES comments(id) ON DELETE CASCADE
);
CREATE TABLE comment_pins (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  target_type VARCHAR(32) NOT NULL,
  target_id BIGINT UNSIGNED NOT NULL,
  comment_id BIGINT UNSIGNED NOT NULL,
  pinned_by BIGINT UNSIGNED NOT NULL,
  pinned_at DATETIME(3) NOT NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_comment_pin_target (target_type, target_id),
  CONSTRAINT fk_comment_pins_comment_id FOREIGN KEY (comment_id) REFERENCES comments(id) ON DELETE CASCADE
);
