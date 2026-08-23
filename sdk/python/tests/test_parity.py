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
    """Every public callable, including classmethods.

    `predicate=inspect.isfunction` skips classmethods and properties, so the
    first version of this guard did not cover `connect` or `url` — the two
    members somebody reaches for first. A drift test with a hole in it is worse
    than none, because it is believed.
    """
    out = {}
    for name in dir(cls):
        if name.startswith("_"):
            continue
        member = inspect.getattr_static(cls, name)
        if isinstance(member, classmethod):
            out[name] = inspect.signature(member.__func__)
        elif inspect.isfunction(member):
            out[name] = inspect.signature(member)
    return out


def _public_properties(cls) -> set[str]:
    return {n for n in dir(cls)
            if not n.startswith("_") and isinstance(inspect.getattr_static(cls, n), property)}


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


def test_properties_exist_on_both_sides():
    # `url` is a property, and a property that exists on one side only is exactly
    # the drift this file is for.
    for sync_cls, async_cls in PAIRS:
        missing = sorted(_public_properties(sync_cls) - _public_properties(async_cls)
                         - set(_public_methods(async_cls)) - set(vars(async_cls)))
        assert not missing, f"{async_cls.__name__} is missing {missing}"


def test_the_async_methods_are_actually_async():
    for _sync_cls, async_cls in PAIRS:
        for name, _ in _public_methods(async_cls).items():
            fn = inspect.getattr_static(async_cls, name)
            fn = fn.__func__ if isinstance(fn, classmethod) else fn
            assert inspect.iscoroutinefunction(fn), f"{async_cls.__name__}.{name} is not async"
