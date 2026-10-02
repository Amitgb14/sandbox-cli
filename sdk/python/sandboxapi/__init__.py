"""Python client for Sandbox API v1 (docs/api/v1.md).

Standard library only. Talks to any endpoint — a local sandboxd on a unix
socket, a self-hosted one over TLS, or the cloud — and cannot tell which,
beyond what ``capabilities()`` says.

    from sandboxapi import Client
    c = Client("unix:///run/user/1000/sandboxd.sock")
    sb = c.create_sandbox(network={"mode": "none"})
    print(c.run(sb["id"], ["echo", "hello"])["stdout"])
    c.terminate_sandbox(sb["id"])
"""

from .client import ApiError, Client

__all__ = ["ApiError", "Client"]
__version__ = "0.1.0"
