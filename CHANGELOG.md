# Changelog

## [0.2.1](https://github.com/newstack-cloud/celerity-go-sdk/compare/v0.2.0...v0.2.1) (2026-10-09)


### Bug Fixes

* correct release process to be compatible with release please ([45fa6ae](https://github.com/newstack-cloud/celerity-go-sdk/commit/45fa6ae53861446917994321b027d7a0143075e7))
* update warm proxy script to force 0.2.1 due to broken 0.2.0 release ([aaf23d3](https://github.com/newstack-cloud/celerity-go-sdk/commit/aaf23d30e67442be21637f016ad3452496b7a1a1))

## [0.2.0](https://github.com/newstack-cloud/celerity-go-sdk/compare/v0.1.0...v0.2.0) (2026-10-08)


### Features

* **core:** add core config implementation ([39774e9](https://github.com/newstack-cloud/celerity-go-sdk/commit/39774e92ccdb96d22170825bbc335ffaffaeccd9))
* **core:** add core instrumentation hook for sql databases ([5b77dc1](https://github.com/newstack-cloud/celerity-go-sdk/commit/5b77dc18d2b743533a06dbc2eebe3b42b2254fde))
* **core:** add core resource abstractions ([3e6053e](https://github.com/newstack-cloud/celerity-go-sdk/commit/3e6053e3cff0d1c0fdee513c453a547607c66acc))
* **core:** add data store resource abstraction and core resource helpers ([db8e457](https://github.com/newstack-cloud/celerity-go-sdk/commit/db8e457ecdfdab1a4e64f8749f718f979034ad5d))
* **core:** add interface and listing helpers for buckets ([3283d48](https://github.com/newstack-cloud/celerity-go-sdk/commit/3283d48fff540f69fa6b6f5db2049502b422f138))
* **core:** add manifest extraction and dependency linking codegen ([1666896](https://github.com/newstack-cloud/celerity-go-sdk/commit/16668969a398e38da99dcdf7d7a6b66aad48af2f))
* **core:** add reusable field reader for related configuration ([8ffdd97](https://github.com/newstack-cloud/celerity-go-sdk/commit/8ffdd97d50bf77f8507746756b499590b98a877d))
* **core:** add support for flushing tracing spans ([95726e3](https://github.com/newstack-cloud/celerity-go-sdk/commit/95726e34d1a06e155ffc22ef37c207094309bbe9))
* **core:** add test utils for celerity application developers ([415804b](https://github.com/newstack-cloud/celerity-go-sdk/commit/415804b42823e882832e589d4116c5c7228fb391))
* **core:** integrate core telemetry ([1b44b70](https://github.com/newstack-cloud/celerity-go-sdk/commit/1b44b7003b306882df3252eadc969317c09958bb))
* **core:** integrate tracing span flushing for containerised environments ([c2a333a](https://github.com/newstack-cloud/celerity-go-sdk/commit/c2a333a36fb2056da40f96fa9d7305daf8dd2117))
* **core:** make room for a serverless adapter to be written against ([2f21e6d](https://github.com/newstack-cloud/celerity-go-sdk/commit/2f21e6dff76fa49772473e6b8cfca33985683056))
* **core:** serve websockets, answer with detail, and take wiring from the blueprint ([19a9d85](https://github.com/newstack-cloud/celerity-go-sdk/commit/19a9d8583d199b20f75b0a1ceb023b9832dc6896))
* **resources-aws:** add dynamodb implementation of the data store resource ([d3f9c7d](https://github.com/newstack-cloud/celerity-go-sdk/commit/d3f9c7d56cd4f0f2d449c4643f71234165c0cf09))
* **serverless-aws:** run Celerity handlers on AWS Lambda ([c9e8a84](https://github.com/newstack-cloud/celerity-go-sdk/commit/c9e8a8444fcf82e32c94996842e58688a046d7ea))


### Bug Fixes

* **core:** add fix to cache stub scan in celerity test helpers ([7f95894](https://github.com/newstack-cloud/celerity-go-sdk/commit/7f9589493ac31c3742c526999b634ec04ba12eb4))
* **core:** add optional func path to go-specific manifest ([2347d06](https://github.com/newstack-cloud/celerity-go-sdk/commit/2347d06f1183ef899eeb5911bb8fd895ba354de1))
* **core:** bind path, query and header parameters into a pointer input ([24d6ddf](https://github.com/newstack-cloud/celerity-go-sdk/commit/24d6ddf98da475516787a8716575bced4acee815))
* **core:** ensure local providers are not included in production builds ([cae6ba1](https://github.com/newstack-cloud/celerity-go-sdk/commit/cae6ba1b159945817a83a99eafd3d5a5a1b366e8))
* **core:** keep the validation library's own text out of what a caller reads ([da4c23b](https://github.com/newstack-cloud/celerity-go-sdk/commit/da4c23b4bc04bceba8d2b259841c046fc62aac1f))
* **core:** refuse an event carrying a source the handler does not serve ([fefae1f](https://github.com/newstack-cloud/celerity-go-sdk/commit/fefae1f03e709d9278d28a9e196344e3a10ebe7e))
* **core:** remove delay from publish send options ([051cb70](https://github.com/newstack-cloud/celerity-go-sdk/commit/051cb70f3bc86d78bff98f5fc5816f1b8884ba03))
