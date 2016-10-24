import os
from importlib import import_module
try:
    from setuptools import setup
except ImportError:
    from distutils.core import setup


basedir = os.path.abspath(os.path.dirname(__file__) or '.')

# required data

package_name = 'fakejuju'
NAME = package_name
SUMMARY = 'A limited adaptation of Juju\'s client, with testing hooks.'
AUTHOR = 'Canonical Landscape team'
EMAIL = 'juju@lists.ubuntu.com'
PROJECT_URL = 'https://launchpad.net/fake-juju'
LICENSE = 'LGPLv3'

with open(os.path.join(basedir, 'README.md')) as readme_file:
    DESCRIPTION = readme_file.read()

# dymanically generated data

VERSION = import_module(package_name).__version__

# set up packages

exclude_dirs = [
        'tests',
        ]

PACKAGES = []
for path, dirs, files in os.walk(package_name):
    if "__init__.py" not in files:
        continue
    path = path.split(os.sep)
    if path[-1] in exclude_dirs:
        continue
    PACKAGES.append(".".join(path))

# dependencies

DEPS = ['fixtures',
        'testtools',
        'txjuju',
        'twisted',
        'yaml',
        ]


if __name__ == "__main__":
    setup(name=NAME,
          version=VERSION,
          author=AUTHOR,
          author_email=EMAIL,
          url=PROJECT_URL,
          license=LICENSE,
          description=SUMMARY,
          long_description=DESCRIPTION,
          packages=PACKAGES,

          # for distutils
          requires=DEPS,

          # for setuptools
          install_requires=DEPS,
          )
