# Changelog

## [0.2.0](https://github.com/newstack-cloud/celerity-go-sdk/compare/v0.1.0...v0.2.0) (2026-09-22)


### Features

* **core:** add core config implementation ([39774e9](https://github.com/newstack-cloud/celerity-go-sdk/commit/39774e92ccdb96d22170825bbc335ffaffaeccd9))
* **core:** make room for a serverless adapter to be written against ([2f21e6d](https://github.com/newstack-cloud/celerity-go-sdk/commit/2f21e6dff76fa49772473e6b8cfca33985683056))
* **core:** serve websockets, answer with detail, and take wiring from the blueprint ([19a9d85](https://github.com/newstack-cloud/celerity-go-sdk/commit/19a9d8583d199b20f75b0a1ceb023b9832dc6896))
* **serverless-aws:** run Celerity handlers on AWS Lambda ([c9e8a84](https://github.com/newstack-cloud/celerity-go-sdk/commit/c9e8a8444fcf82e32c94996842e58688a046d7ea))


### Bug Fixes

* **core:** bind path, query and header parameters into a pointer input ([24d6ddf](https://github.com/newstack-cloud/celerity-go-sdk/commit/24d6ddf98da475516787a8716575bced4acee815))
* **core:** ensure local providers are not included in production builds ([cae6ba1](https://github.com/newstack-cloud/celerity-go-sdk/commit/cae6ba1b159945817a83a99eafd3d5a5a1b366e8))
* **core:** keep the validation library's own text out of what a caller reads ([da4c23b](https://github.com/newstack-cloud/celerity-go-sdk/commit/da4c23b4bc04bceba8d2b259841c046fc62aac1f))
* **core:** refuse an event carrying a source the handler does not serve ([fefae1f](https://github.com/newstack-cloud/celerity-go-sdk/commit/fefae1f03e709d9278d28a9e196344e3a10ebe7e))
