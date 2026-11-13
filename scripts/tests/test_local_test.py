"""Engine selection must never reinterpret an existing private environment."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
spec = importlib.util.spec_from_file_location('local_test', Path(__file__).resolve().parents[1] / 'local_test.py')
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)

class EngineSelection(unittest.TestCase):
    def test_new_mysql_and_explicit_postgres(self):
        with patch.dict('os.environ', {}, clear=True):
            self.assertEqual(local.database_driver({'LOCAL_TEST_DATABASE_DRIVER': 'mysql'}), 'mysql')
            self.assertEqual(local.database_driver({'LOCAL_TEST_DATABASE_DRIVER': 'postgres'}), 'postgres')
            with self.assertRaisesRegex(RuntimeError, 'No automatic engine switch'):
                local.database_driver({})
        with patch.dict('os.environ', {'AUTHOVA_LOCAL_TEST_DATABASE_DRIVER': 'postgres'}, clear=True):
            self.assertEqual(local.database_driver({}), 'postgres')
            with self.assertRaisesRegex(RuntimeError, 'new private state'):
                local.database_driver({'LOCAL_TEST_DATABASE_DRIVER': 'mysql'})

if __name__ == '__main__':
    unittest.main()
