import importlib.util
import io
from pathlib import Path
import unittest
import urllib.error
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location("image_contracts", Path(__file__).with_name("check-image-contracts.py"))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)


class Response(io.BytesIO):
    status = 200


class ReadinessTests(unittest.TestCase):
    def test_startup_transport_failures_retry_before_success(self):
        opener = Mock(side_effect=[ConnectionResetError("startup"), urllib.error.URLError("refused"),
                                   TimeoutError("startup"), Response(b'{"status":"ready"}')])
        sleep = Mock()
        self.assertEqual(checks.wait_for_health("http://fixture/v1/health", opener=opener, sleep=sleep), {"status": "ready"})
        self.assertEqual(opener.call_count, 4)
        self.assertEqual(sleep.call_count, 3)

    def test_persistent_reset_stays_bounded(self):
        opener = Mock(side_effect=ConnectionResetError("not ready"))
        with self.assertRaisesRegex(AssertionError, "did not become ready"):
            checks.wait_for_health("http://fixture/v1/health", attempts=3, opener=opener, sleep=Mock())
        self.assertEqual(opener.call_count, 3)

    def test_invalid_application_response_does_not_get_hidden(self):
        opener = Mock(return_value=Response(b'not JSON'))
        with self.assertRaises(ValueError):
            checks.wait_for_health("http://fixture/v1/health", opener=opener, sleep=Mock())
        self.assertEqual(opener.call_count, 1)


if __name__ == "__main__":
    unittest.main()
