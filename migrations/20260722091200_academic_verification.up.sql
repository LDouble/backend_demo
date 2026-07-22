-- module: academic_verification
CREATE TABLE academic_identities (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  student_no VARCHAR(64) NOT NULL,
  real_name VARCHAR(100) NOT NULL,
  method VARCHAR(24) NOT NULL,
  status VARCHAR(24) NOT NULL,
  verified_at DATETIME(3) NOT NULL,
  revoked_at DATETIME(3) NULL,
  revoke_reason VARCHAR(500) NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_academic_identity_user (user_id),
  UNIQUE KEY uk_academic_identity_student_no (student_no)
);
CREATE TABLE academic_verification_materials (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  storage_key VARCHAR(128) NOT NULL,
  mime_type VARCHAR(32) NOT NULL,
  size_bytes BIGINT NOT NULL,
  sha256 VARCHAR(64) NOT NULL,
  status VARCHAR(24) NOT NULL,
  bound_request_id BIGINT UNSIGNED NULL,
  expires_at DATETIME(3) NOT NULL,
  delete_after DATETIME(3) NULL,
  deleted_at DATETIME(3) NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_academic_material_storage_key (storage_key),
  KEY idx_academic_material_cleanup (status, expires_at, delete_after)
);
CREATE TABLE academic_verification_requests (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  student_no VARCHAR(64) NOT NULL,
  real_name VARCHAR(100) NOT NULL,
  method VARCHAR(24) NOT NULL,
  material_id BIGINT UNSIGNED NULL,
  status VARCHAR(24) NOT NULL,
  review_reason VARCHAR(500) NULL,
  reviewed_by BIGINT UNSIGNED NULL,
  reviewed_at DATETIME(3) NULL,
  version BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_academic_request_review (status, user_id)
);
