"""The async face must not drift from the sync one.

The design is one implementation with a thin async facade, and the whole risk of
that shape is a method appearing on one side only — at which point the promise
"they are the same" quietly stops being true. This is that promise, as a test.
"""

import inspect

from sandbox_cli import Project, Studio, Workspace
from sandbox_cli.aio import AsyncProject, AsyncStudio, AsyncWorkspace

PAIRS = [(Studio, AsyncStudio), (Project, AsyncProject), (Workspace, AsyncWorkspace)]


def _public_methods(cls) -> dict[str, inspect.Signature]:
    out = {}
    for name, member in inspect.getmembers(cls, predicate=inspect.isfunction):
        if name.startswith("_"):
            continue
        out[name] = inspect.signature(member)
    return out


def test_every_sync_method_has_an_async_twin():
    for sync_cls, async_cls in PAIRS:
        sync = _public_methods(sync_cls)
        asyn = _public_methods(async_cls)
        missing = sorted(set(sync) - set(asyn))
        assert not missing, f"{async_cls.__name__} is missing {missing}"


def test_the_async_twin_takes_the_same_arguments():
    for sync_cls, async_cls in PAIRS:
        for name, sig in _public_methods(sync_cls).items():
            other = _public_methods(async_cls)[name]
            # Names and kinds, not annotations: the async side returns awaitables
            # and its parameters are the same question asked the same way.
            assert [p.name for p in sig.parameters.values()] == \
                   [p.name for p in other.parameters.values()], \
                f"{async_cls.__name__}.{name} takes different arguments"


def test_the_async_methods_are_actually_async():
    for _sync_cls, async_cls in PAIRS:
        for name, _ in _public_methods(async_cls).items():
            fn = getattr(async_cls, name)
            assert inspect.iscoroutinefunction(fn), f"{async_cls.__name__}.{name} is not async"
