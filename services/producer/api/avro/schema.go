package avroschema

import _ "embed"

// Text is the MovieEvent schema. The init process registers these exact bytes, and the producer encodes with them, so the schema id matches the payload.
//
//go:embed movie_event.avsc
var Text string
