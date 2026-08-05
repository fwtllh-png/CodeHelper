import { compatibility } from "./generated.js";
import type { BinaryVersion } from "../runtime/process.js";

export function assertCompatibleBinary(
  binary: BinaryVersion,
  development: boolean,
  platform = process.platform,
  architecture = process.arch,
): void {
  const expectedOS = platform === "win32" ? "windows" : platform;
  const expectedArchitecture = architecture === "x64" ? "amd64" : architecture;
  if (!compatibility.targets.some(
    (target) => target.os === binary.os && target.arch === binary.arch,
  ) || binary.os !== expectedOS || binary.arch !== expectedArchitecture) {
    throw new Error(
      `CodeHelper binary target ${binary.os}/${binary.arch} does not match ` +
      `${expectedOS}/${expectedArchitecture}`,
    );
  }
  if (binary.acpProtocolMin > compatibility.acp_protocol.max ||
    binary.acpProtocolMax < compatibility.acp_protocol.min ||
    binary.operationSchemaVersion !== compatibility.operation_schema_version) {
    throw new Error("CodeHelper binary protocol is incompatible with this extension");
  }
  if (binary.version === "dev") {
    if (!development) {
      throw new Error("development CodeHelper binary is unavailable in production");
    }
    return;
  }
  if (!satisfiesRange(binary.version, compatibility.binary_version_range)) {
    throw new Error(
      `CodeHelper binary ${binary.version} is outside ` +
      compatibility.binary_version_range,
    );
  }
}

export function satisfiesRange(version: string, range: string): boolean {
  const match = /^>=([0-9]+\.[0-9]+\.[0-9]+) <([0-9]+\.[0-9]+\.[0-9]+)$/u.exec(range);
  const candidate = semanticVersion(version);
  const minimumValue = match?.[1];
  const maximumValue = match?.[2];
  if (minimumValue === undefined || maximumValue === undefined ||
    candidate === undefined) {
    return false;
  }
  const minimum = semanticVersion(minimumValue);
  const maximum = semanticVersion(maximumValue);
  return minimum !== undefined && maximum !== undefined &&
    compare(candidate, minimum) >= 0 && compare(candidate, maximum) < 0;
}

function semanticVersion(value: string): readonly [number, number, number] | undefined {
  const match = /^([0-9]+)\.([0-9]+)\.([0-9]+)$/u.exec(value);
  if (match === null) {
    return undefined;
  }
  const major = match[1];
  const minor = match[2];
  const patch = match[3];
  if (major === undefined || minor === undefined || patch === undefined) {
    return undefined;
  }
  return [
    Number.parseInt(major, 10),
    Number.parseInt(minor, 10),
    Number.parseInt(patch, 10),
  ];
}

function compare(
  left: readonly [number, number, number],
  right: readonly [number, number, number],
): number {
  const [leftMajor, leftMinor, leftPatch] = left;
  const [rightMajor, rightMinor, rightPatch] = right;
  for (const difference of [
    leftMajor - rightMajor,
    leftMinor - rightMinor,
    leftPatch - rightPatch,
  ]) {
    if (difference !== 0) {
      return difference;
    }
  }
  return 0;
}
