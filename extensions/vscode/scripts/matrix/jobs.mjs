export const matrixJobs = Object.freeze([
  required("local-darwin-arm64-external", "macOS arm64 external single+multi"),
  required("local-darwin-arm64-bundled", "macOS arm64 bundled handshake"),
  required("local-darwin-x64-external", "macOS x64 external single+multi (Rosetta)"),
  required("update-integration", "signed update, rollback, revocation"),
  required("distribution", "universal and target VSIX distribution"),
  required("security", "extension security gate"),
  required("performance", "extension performance gate"),
  optional("local-win32-x64", "Windows x64 runner unavailable"),
]);

export const requiredMatrixJobNames = Object.freeze(
  matrixJobs.filter((job) => job.required).map((job) => job.job),
);

function required(job, description) {
  return Object.freeze({ job, description, required: true });
}

function optional(job, description) {
  return Object.freeze({ job, description, required: false });
}
