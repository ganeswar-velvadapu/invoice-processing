ALTER TABLE document_extractions DROP COLUMN IF EXISTS extraction_model;
DELETE FROM document_extractions WHERE extraction_method = 'VLM';
ALTER TABLE document_extractions DROP CONSTRAINT IF EXISTS document_extractions_extraction_method_check;
ALTER TABLE document_extractions ADD CONSTRAINT document_extractions_extraction_method_check
    CHECK (extraction_method IN ('PDF_TEXT','OCR','MANUAL'));
