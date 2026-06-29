FROM ubuntu:24.04
COPY boot-id-token.service /etc/systemd/system/boot-id-token.service
COPY vabbe-rshared.service /etc/systemd/system/vabbe-rshared.service
COPY vabbe-resolv.service /etc/systemd/system/vabbe-resolv.service
COPY vabbe-resolv /usr/local/sbin/vabbe-resolv
COPY vabbe-kmod.service /etc/systemd/system/vabbe-kmod.service
COPY vabbe-kmod /usr/local/sbin/vabbe-kmod
RUN apt-get update && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus \
      openssh-server sudo python3 python3-apt \
      curl ca-certificates gnupg apt-transport-https \
      iproute2 iputils-ping kmod iptables ipset ipvsadm \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh \
 && chmod +x /usr/local/sbin/vabbe-resolv /usr/local/sbin/vabbe-kmod \
 && systemctl enable ssh \
 && systemctl enable boot-id-token.service \
 && systemctl enable vabbe-rshared.service \
 && systemctl enable vabbe-resolv.service \
 && systemctl enable vabbe-kmod.service \
 && printf 'PermitRootLogin prohibit-password\nPasswordAuthentication no\nStrictModes no\n' \
      > /etc/ssh/sshd_config.d/vabbe.conf
RUN systemctl mask systemd-udevd.service systemd-udev-trigger.service \
      sys-kernel-debug.mount sys-kernel-tracing.mount \
      systemd-journald-audit.socket getty@tty1.service console-getty.service \
      2>/dev/null || true
STOPSIGNAL SIGRTMIN+3
VOLUME ["/var/lib/containerd"]
CMD ["/sbin/init"]
