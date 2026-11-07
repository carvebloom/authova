// Presentation only. Every proof is checked against the current server policy.
(() => {
  const form = document.querySelector('form[data-otp-policy]');
  if (!form) return;
  const purpose = form.querySelector('select[name="purpose"]');
  const channel = form.querySelector('select[name="channel"]');
  if (!purpose || !channel) return;
  const otpOnly = form.dataset.otpOnlyRegistration === 'true';
  const update = () => {
    const restricted = purpose.value === 'login' || (purpose.value === 'register' && otpOnly);
    for (const option of channel.options) {
      const enabled = option.value === 'email' ? form.dataset.emailOtp : form.dataset.phoneOtp;
      option.disabled = restricted && enabled !== 'true';
    }
    if (channel.selectedOptions[0]?.disabled) {
      channel.value = Array.from(channel.options).find(option => !option.disabled)?.value ?? '';
    }
  };
  purpose.addEventListener('change', update);
  update();
})();
