/**
 * The gRPC source's wire contract, as text the form can show and copy.
 *
 * It is a copy of pkg/comm/source/grpc/proto/source.proto. A copy can drift, so
 * grpcSourceGuide.test.tsx compares it with that file byte for byte; change the
 * .proto and this together.
 */
export const GRPC_SOURCE_PROTO = `syntax = "proto3";

package hermod.source.grpc.v1;

option go_package = "github.com/gsoultan/hermod/pkg/comm/source/grpc/proto";

// SourceService is how a producer pushes records into a Hermod gRPC source.
//
// This file is the whole contract: Hermod serves this one service, and there is
// nowhere to upload a .proto of your own. Generate a client from this file and
// send your record as JSON in PublishRequest.payload.
service SourceService {
  // Publish hands one record to the gRPC source registered for
  // PublishRequest.path. It returns as soon as the record is queued for the
  // workflow, unless the source is set to respond synchronously: then it
  // returns when the workflow has finished with the record, or when the
  // source's response timeout runs out.
  //
  // A source configured with an API key requires it as "x-api-key" metadata.
  rpc Publish(PublishRequest) returns (PublishResponse);
}

message PublishRequest {
  // The path configured on the gRPC source, sent exactly as configured — for
  // example "/grpc/orders". It is a routing label, not a URL. Empty means
  // "/grpc/default". The source only receives while its workflow is running.
  string path = 1;
  // Record ID. Hermod generates one when this is empty.
  string id = 2;
  // Optional change type: "create", "update", "delete" or "snapshot". When it
  // is set the record is treated as a change event, and its fields are read as
  // "after.<field>" in the workflow rather than as "<field>".
  string operation = 3;
  // Optional table and schema the record belongs to.
  string table = 4;
  string schema = 5;
  // Optional previous row image for a change event, as a JSON object.
  bytes before = 6;
  // The record, as a JSON object. Used when payload is empty.
  bytes after = 7;
  // The record, as a JSON object. Takes precedence over after.
  //
  // These are bytes: a generated client passes the JSON text as-is, and a
  // JSON-speaking tool such as grpcurl or "buf curl" needs it base64-encoded.
  bytes payload = 8;
  // Free-form string metadata carried alongside the record.
  map<string, string> metadata = 9;
}

message PublishResponse {
  // The record's ID: the one sent, or the one Hermod generated.
  string id = 1;
  // "dispatched" when the record was queued for the workflow. A source that
  // responds synchronously reports what the workflow did instead: "delivered",
  // "completed" (it ran and had nothing to write), "filtered" (a filter dropped
  // it on purpose), "dead_lettered" or "failed", or "pending" when the wait ran
  // out before the workflow finished.
  string status = 2;
  // Why the record failed. Set for "dead_lettered" and "failed".
  string error = 3;
  // The record as the workflow left it, as a JSON object. Set by a source that
  // responds synchronously.
  bytes record = 4;
}
`;

/** base64 of {"order_id":1} — `payload` is a bytes field. */
export const SAMPLE_PAYLOAD_JSON = '{"order_id":1}';
export const SAMPLE_PAYLOAD_BASE64 = 'eyJvcmRlcl9pZCI6MX0=';

/** The path a client is told to send when the form has none yet. */
export const GRPC_EXAMPLE_PATH = '/grpc/my-source';

/**
 * grpcPublishCommand builds a `buf curl` call for the given source path. A keyed
 * source gets the metadata flag with a placeholder: the command is rendered on
 * screen and copied into terminals, so the key itself never goes into it.
 */
export function grpcPublishCommand(path: string, hasApiKey: boolean): string {
  const body = JSON.stringify({ path: path || GRPC_EXAMPLE_PATH, payload: SAMPLE_PAYLOAD_BASE64 });
  return [
    'buf curl --protocol grpc --http2-prior-knowledge --schema source.proto \\',
    ...(hasApiKey ? ["  -H 'x-api-key: YOUR_API_KEY' \\"] : []),
    `  -d '${body}' \\`,
    '  http://HERMOD_HOST:50051/hermod.source.grpc.v1.SourceService/Publish',
  ].join('\n');
}
