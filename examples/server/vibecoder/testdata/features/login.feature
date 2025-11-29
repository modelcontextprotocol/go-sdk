Feature: Login
  Scenario: Successful Login
    Given I have a valid user
    When I login
    Then I am redirected
