"""Make the package importable from a checkout without installing it.

An editable install would be tidier and needs a build backend present; this keeps
`pytest` working straight after `git clone`, which is when somebody is most
likely to run it.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
