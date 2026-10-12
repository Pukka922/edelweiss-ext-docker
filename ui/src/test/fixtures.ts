import type { ContainerInfo } from "../api";

/** A running container "web" as GET /containers/web answers (the fixture of the container pages' tests). */
export const info: ContainerInfo = {
  container: {
    id: "aaaaaaaaaaaaaaaa",
    name: "web",
    image: "nginx:1.29",
    imageId: "sha256:n",
    created: "2026-10-10T12:00:00Z",
    state: {
      status: "running",
      running: true,
      exitCode: 0,
      startedAt: "2026-10-10T12:00:01Z",
      finishedAt: "0001-01-01T00:00:00Z",
    },
    command: "/docker-entrypoint.sh nginx -g 'daemon off;'",
    ports: [{ hostIp: "127.0.0.1", hostPort: 8080, containerPort: 80, protocol: "tcp" }],
    mounts: [
      { type: "volume", source: "web-data", target: "/data", readOnly: false },
      { type: "bind", source: "/srv/conf", target: "/etc/nginx/conf.d", readOnly: true },
    ],
    networks: [{ name: "bridge", ipv4: "172.17.0.2", gateway: "172.17.0.1", aliases: [] }],
    env: ["PATH=/usr/bin", "TZ=UTC"],
    restart: { policy: "unless-stopped", maxRetries: 0 },
    tty: false,
  },
  spec: {
    name: "web",
    image: "nginx:1.29",
    command: "",
    ports: [],
    mounts: [],
    env: [],
    network: "bridge",
    restart: { policy: "unless-stopped", maxRetries: 0 },
    privileged: false,
  },
};
