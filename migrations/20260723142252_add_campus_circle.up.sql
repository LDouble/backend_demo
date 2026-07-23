-- module: campus_circle
CREATE TABLE campus_circle_sections (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  parent_id BIGINT UNSIGNED NULL,
  slug VARCHAR(64) NOT NULL,
  name VARCHAR(64) NOT NULL,
  description VARCHAR(500) NULL,
  icon_url VARCHAR(2048) NULL,
  cover_url VARCHAR(2048) NULL,
  sort_order BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL,
  created_by BIGINT UNSIGNED NOT NULL,
  updated_by BIGINT UNSIGNED NOT NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_campus_circle_section_slug (slug),
  KEY idx_campus_circle_section_tree (parent_id, status, sort_order),
  CONSTRAINT fk_campus_circle_sections_parent_id FOREIGN KEY (parent_id) REFERENCES campus_circle_sections(id) ON DELETE RESTRICT
);
CREATE TABLE campus_circle_posts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  section_id BIGINT UNSIGNED NOT NULL,
  author_id BIGINT UNSIGNED NOT NULL,
  title VARCHAR(100) NULL,
  content LONGTEXT NULL,
  status VARCHAR(24) NOT NULL,
  review_reason VARCHAR(500) NULL,
  reviewed_by BIGINT UNSIGNED NULL,
  reviewed_at DATETIME(3) NULL,
  published_at DATETIME(3) NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_campus_circle_post_public (status, section_id, published_at),
  KEY idx_campus_circle_post_author (author_id, status),
  KEY idx_campus_circle_post_section (section_id, status),
  CONSTRAINT fk_campus_circle_posts_section_id FOREIGN KEY (section_id) REFERENCES campus_circle_sections(id) ON DELETE RESTRICT
);
CREATE TABLE campus_circle_post_images (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  post_id BIGINT UNSIGNED NOT NULL,
  url VARCHAR(2048) NOT NULL,
  sort_order BIGINT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_campus_circle_post_image_order (post_id, sort_order),
  CONSTRAINT fk_campus_circle_post_images_post_id FOREIGN KEY (post_id) REFERENCES campus_circle_posts(id) ON DELETE CASCADE
);
CREATE TABLE campus_circle_post_likes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  post_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_campus_circle_post_like_user (post_id, user_id),
  KEY idx_campus_circle_post_like_user (user_id),
  CONSTRAINT fk_campus_circle_post_likes_post_id FOREIGN KEY (post_id) REFERENCES campus_circle_posts(id) ON DELETE CASCADE
);
