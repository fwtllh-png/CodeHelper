declare const __CODEHELPER_TEST_BUILD__: boolean;

export const testBuildEnabled =
  typeof __CODEHELPER_TEST_BUILD__ !== "undefined" &&
  __CODEHELPER_TEST_BUILD__;
