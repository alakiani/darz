# A stand-in for a freshly rented Ubuntu 26.04 VM: systemd, SSH and Python,
# nothing else. The playbooks install the rest, as on the real fleet.
FROM ubuntu:26.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus openssh-server python3 sudo ca-certificates curl iproute2 kmod \
 && rm -rf /var/lib/apt/lists/* \
 && systemctl enable ssh.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
