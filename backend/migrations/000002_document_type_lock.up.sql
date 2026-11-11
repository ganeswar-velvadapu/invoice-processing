-- A type chosen by a reviewer on re-run is authoritative; an upload hint can still be overridden
-- by a clear document title.
ALTER TABLE documents ADD COLUMN document_type_locked BOOLEAN NOT NULL DEFAULT FALSE;
