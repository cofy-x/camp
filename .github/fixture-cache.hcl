// Two bounded scopes; main and explicit manual qualification may update them.
// PRs only read caches and still build and test on misses.
variable "CACHE_WRITE" {
  default = "false"
}

variable "COMPOSE_PROJECT_NAME" {
  default = "camp-remote-ci"
}

target "server" {
  tags = ["${COMPOSE_PROJECT_NAME}-server"]
  cache-from = ["type=gha,version=2,scope=camp-fixture-server-v1"]
  cache-to = CACHE_WRITE == "true" ? ["type=gha,version=2,scope=camp-fixture-server-v1,mode=max,ignore-error=true"] : []
}

target "tests" {
  tags = ["${COMPOSE_PROJECT_NAME}-tests"]
  cache-from = ["type=gha,version=2,scope=camp-fixture-tests-v1"]
  cache-to = CACHE_WRITE == "true" ? ["type=gha,version=2,scope=camp-fixture-tests-v1,mode=max,ignore-error=true"] : []
}

target "daemon-a" {
  tags = ["${COMPOSE_PROJECT_NAME}-daemon-a"]
  cache-from = ["type=gha,version=2,scope=camp-fixture-tests-v1"]
}

target "daemon-b" {
  tags = ["${COMPOSE_PROJECT_NAME}-daemon-b"]
  cache-from = ["type=gha,version=2,scope=camp-fixture-tests-v1"]
}
