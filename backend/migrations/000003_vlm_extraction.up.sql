-- Extractions produced by the local vision model, with the model recorded for the audit trail.
ALTER TABLE document_extractions DROP CONSTRAINT IF EXISTS document_extractions_extraction_method_check;
ALTER TABLE document_extractions ADD CONSTRAINT document_extractions_extraction_method_check
    CHECK (extraction_method IN ('PDF_TEXT','OCR','MANUAL','VLM'));
ALTER TABLE document_extractions ADD COLUMN extraction_model TEXT;
